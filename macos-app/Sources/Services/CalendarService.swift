import Foundation
import Observation
import EventKit
import AppKit

/// Lazy wrapper around `EKEventStore` for reading and writing macOS Calendar
/// events. Permission is requested the first time a method that needs access
/// is called — never at app launch — so users only see the system prompt when
/// they actually open the agenda or confirm a tool-driven create.
///
/// `EKEventStore` and `EKEvent` are not `Sendable`, so the entire service
/// stays on the main actor. Callers that need to do work off-main with the
/// returned events must extract `Sendable` value-type snapshots first.
@MainActor
@Observable
final class CalendarService {
    /// Shared singleton — there is only ever one EKEventStore per process,
    /// and reusing it preserves the access-grant state across feature surfaces.
    static let shared = CalendarService()

    /// The wrapped EventKit store. We keep it as a single long-lived instance
    /// because EKEventStore caches its database connection internally and is
    /// designed to be reused (Apple docs: "create one event store at a time").
    private let store = EKEventStore()

    /// Cached authorization decision. Populated after the first
    /// `ensureAuthorized()` round-trip so subsequent calls don't ping the
    /// system prompt API again. The system status itself is the source of
    /// truth — this is just a fast-path bool to avoid awaiting on the hot path.
    private(set) var lastKnownStatus: EKAuthorizationStatus

    init() {
        self.lastKnownStatus = EKEventStore.authorizationStatus(for: .event)
    }

    /// Live authorization status. Reflects user grants/revocations made via
    /// System Settings even after the app has launched.
    var authorizationStatus: EKAuthorizationStatus {
        EKEventStore.authorizationStatus(for: .event)
    }

    /// True iff EventKit will let us read AND write to the user's calendars.
    /// macOS 14+ collapsed the old `.authorized` case into `.fullAccess`, but
    /// we still treat both equivalently to remain forward-compatible if Apple
    /// reintroduces the legacy code path on older minor versions.
    var hasFullAccess: Bool {
        switch authorizationStatus {
        case .fullAccess:
            return true
        case .authorized:
            // Legacy alias on older SDKs — treat as full access on macOS 14+.
            return true
        default:
            return false
        }
    }

    // MARK: - Authorization

    /// Triggers the system prompt the first time it's called. Subsequent
    /// calls short-circuit on the cached decision. Returns `true` when we
    /// have full read/write access; `false` for any denied / restricted /
    /// not-determined-but-user-said-no state.
    @discardableResult
    func ensureAuthorized() async -> Bool {
        switch authorizationStatus {
        case .fullAccess, .authorized:
            lastKnownStatus = authorizationStatus
            return true
        case .denied, .restricted:
            lastKnownStatus = authorizationStatus
            return false
        case .notDetermined, .writeOnly:
            // `.writeOnly` is the macOS 14+ "we have a write-only grant" state.
            // We need full access to read upcoming events, so we still prompt
            // for full access here — the system will only re-prompt when the
            // existing grant is narrower than what we ask for.
            do {
                let granted = try await store.requestFullAccessToEvents()
                lastKnownStatus = authorizationStatus
                return granted
            } catch {
                lastKnownStatus = authorizationStatus
                return false
            }
        @unknown default:
            lastKnownStatus = authorizationStatus
            return false
        }
    }

    // MARK: - Reads

    /// Returns all events in the next `hours` window across the default
    /// calendar set (everything the user has visible). Throws
    /// `CalendarServiceError.notAuthorized` if the user has not granted access.
    func upcomingEvents(within hours: Int = 24) async throws -> [EKEvent] {
        guard await ensureAuthorized() else {
            throw CalendarServiceError.notAuthorized
        }
        let now = Date()
        let end = Calendar.current.date(byAdding: .hour, value: hours, to: now) ?? now.addingTimeInterval(TimeInterval(hours) * 3600)

        // `nil` calendars = the default visible set, which is what users
        // expect when we say "your agenda" — it respects their per-calendar
        // visibility toggles in Calendar.app.
        let predicate = store.predicateForEvents(
            withStart: now,
            end: end,
            calendars: nil
        )
        let events = store.events(matching: predicate)
        return events.sorted { ($0.startDate ?? .distantFuture) < ($1.startDate ?? .distantFuture) }
    }

    // MARK: - Writes

    /// Creates and saves a new event. The caller is responsible for having
    /// already shown a confirmation UI — this service performs no implicit
    /// user-facing prompts beyond the system permission grant.
    ///
    /// - Parameter calendar: when nil, uses `defaultCalendarForNewEvents`.
    @discardableResult
    func createEvent(
        title: String,
        start: Date,
        end: Date,
        notes: String? = nil,
        calendar: EKCalendar? = nil
    ) async throws -> EKEvent {
        guard await ensureAuthorized() else {
            throw CalendarServiceError.notAuthorized
        }
        guard end > start else {
            throw CalendarServiceError.invalidTimeRange
        }
        guard let target = calendar ?? store.defaultCalendarForNewEvents else {
            throw CalendarServiceError.noWritableCalendar
        }

        let event = EKEvent(eventStore: store)
        event.title = title
        event.startDate = start
        event.endDate = end
        event.notes = notes
        event.calendar = target

        do {
            try store.save(event, span: .thisEvent, commit: true)
        } catch {
            throw CalendarServiceError.saveFailed(error.localizedDescription)
        }
        return event
    }

    // MARK: - Calendar lookup

    /// Returns the writable calendar matching `name` (case-insensitive), or
    /// nil. Used by the `create_calendar_event` tool when the LLM specifies
    /// `calendar_name`. We only return calendars that allow modifications so
    /// the save call doesn't blow up later with a permission error.
    func writableCalendar(named name: String) -> EKCalendar? {
        let cals = store.calendars(for: .event)
        return cals.first { cal in
            cal.allowsContentModifications &&
            cal.title.compare(name, options: .caseInsensitive) == .orderedSame
        }
    }

    // MARK: - Snapshots for the web bridge

    /// Sendable, JSON-encodable list of the user's calendars, for the WebUI's
    /// calendar picker. Triggers the permission prompt the first time it runs;
    /// returns an empty list if access is denied.
    func calendarSnapshots() async -> [CalendarSnapshot] {
        guard await ensureAuthorized() else { return [] }
        return store.calendars(for: .event).map { c in
            CalendarSnapshot(
                id: c.calendarIdentifier,
                title: c.title,
                color: Self.hexColor(c.color),
                writable: c.allowsContentModifications
            )
        }
    }

    /// Sendable, JSON-encodable upcoming events, optionally narrowed to a set
    /// of calendar identifiers (empty = all visible calendars). EKEvent isn't
    /// Sendable, so we flatten to value types here on the main actor.
    func eventSnapshots(within hours: Int, calendarIDs: [String]) async throws -> [EventSnapshot] {
        guard await ensureAuthorized() else {
            throw CalendarServiceError.notAuthorized
        }
        let now = Date()
        let end = Calendar.current.date(byAdding: .hour, value: hours, to: now)
            ?? now.addingTimeInterval(TimeInterval(hours) * 3600)
        let cals: [EKCalendar]? = calendarIDs.isEmpty
            ? nil
            : store.calendars(for: .event).filter { calendarIDs.contains($0.calendarIdentifier) }
        let predicate = store.predicateForEvents(withStart: now, end: end, calendars: cals)
        let iso = ISO8601DateFormatter()
        return store.events(matching: predicate)
            .sorted { ($0.startDate ?? .distantFuture) < ($1.startDate ?? .distantFuture) }
            .map { e in
                EventSnapshot(
                    title: e.title ?? "(no title)",
                    start: e.startDate.map { iso.string(from: $0) } ?? "",
                    end: e.endDate.map { iso.string(from: $0) } ?? "",
                    location: e.location ?? "",
                    notes: e.notes ?? "",
                    calendarId: e.calendar?.calendarIdentifier ?? "",
                    calendarTitle: e.calendar?.title ?? "",
                    attendees: (e.attendees ?? []).compactMap { $0.name },
                    allDay: e.isAllDay
                )
            }
    }

    /// EKCalendar.color is an NSColor on macOS; convert to "#RRGGBB" for the
    /// web layer. Falls back to a neutral grey for unusual colour spaces.
    private static func hexColor(_ color: NSColor?) -> String {
        guard let rgb = color?.usingColorSpace(.sRGB) else { return "#8A8A8A" }
        let r = Int((rgb.redComponent * 255).rounded())
        let g = Int((rgb.greenComponent * 255).rounded())
        let b = Int((rgb.blueComponent * 255).rounded())
        return String(format: "#%02X%02X%02X", r, g, b)
    }
}

/// Sendable snapshot of an `EKCalendar` for the JS↔Swift bridge.
struct CalendarSnapshot: Sendable, Encodable {
    let id: String
    let title: String
    let color: String
    let writable: Bool
}

/// Sendable snapshot of an `EKEvent` for the JS↔Swift bridge.
struct EventSnapshot: Sendable, Encodable {
    let title: String
    let start: String
    let end: String
    let location: String
    let notes: String
    let calendarId: String
    let calendarTitle: String
    let attendees: [String]
    let allDay: Bool
}

/// Typed errors so the chat layer can render context-specific messages
/// (e.g. nudge to Settings on `.notAuthorized`).
enum CalendarServiceError: LocalizedError {
    case notAuthorized
    case invalidTimeRange
    case noWritableCalendar
    case saveFailed(String)

    var errorDescription: String? {
        switch self {
        case .notAuthorized:
            return "Calendar access has not been granted. Open System Settings → Privacy & Security → Calendars to enable Hygur."
        case .invalidTimeRange:
            return "End date must be after the start date."
        case .noWritableCalendar:
            return "No writable calendar is available. Pick a calendar in Calendar.app first."
        case .saveFailed(let detail):
            return "Failed to save the calendar event: \(detail)"
        }
    }
}
