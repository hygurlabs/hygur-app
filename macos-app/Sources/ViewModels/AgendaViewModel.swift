import Foundation
import Observation
import EventKit

/// Read-only snapshot of an `EKEvent` we feel comfortable handing across
/// boundaries. EventKit's own types aren't `Sendable` and Swift 6 strict
/// concurrency rejects passing them through `@Observable` properties without
/// the @MainActor isolation propagating everywhere — so we mirror just the
/// fields the UI needs.
struct CalendarEventSnapshot: Identifiable, Hashable, Sendable {
    let id: String
    let title: String
    let startDate: Date
    let endDate: Date
    let isAllDay: Bool
    let calendarTitle: String
    let calendarColorHex: UInt32?
    let location: String?
    let notes: String?

    /// Day bucket used for grouping in the UI (start-of-day in the user's
    /// current calendar). Two events on the same day land in the same bucket.
    var dayKey: Date {
        Calendar.current.startOfDay(for: startDate)
    }
}

@MainActor
@Observable
final class AgendaViewModel {
    /// LLM-extracted urgent actions (existing behaviour — kept untouched).
    var actions: [AgendaAction] = []
    /// Real macOS Calendar events in the next 48 h. Empty when permission has
    /// not been granted (we surface a permission CTA instead of failing loudly).
    var calendarEvents: [CalendarEventSnapshot] = []
    /// Live mirror of the EventKit auth status so the sheet can render the
    /// "Open Privacy Settings" inline message when access is denied.
    var calendarAuthorizationStatus: EKAuthorizationStatus = EKEventStore.authorizationStatus(for: .event)
    var isLoading = false
    var error: String?

    private let service: SidecarService
    private let calendarService: CalendarService

    init(
        service: SidecarService = .fromSettings(),
        calendarService: CalendarService = .shared
    ) {
        self.service = service
        self.calendarService = calendarService
    }

    /// Default refresh used by ambient surfaces (chat view's badge). Pulls
    /// actions and calendar events but does NOT prompt for calendar access —
    /// users who haven't granted yet won't see a system dialog just for
    /// opening the chat. The sheet itself uses `refreshCalendar(prompt:true)`
    /// so the prompt only appears when the user explicitly opens "Agenda".
    func refresh() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let resp = try await service.agendaContext()
            actions = resp.actions
        } catch {
            self.error = error.localizedDescription
        }
        await refreshCalendar(prompt: false)
    }

    /// Pulls the next 48h of events from EventKit and converts them to
    /// `Sendable` snapshots. When `prompt` is false, silently no-ops on
    /// `.notDetermined` instead of triggering the system permission dialog.
    /// Existing grants (`.fullAccess` / `.authorized`) are honoured in both
    /// modes so a returning user still sees their calendar in the badge.
    func refreshCalendar(prompt: Bool = true) async {
        if !prompt && calendarService.authorizationStatus == .notDetermined {
            calendarAuthorizationStatus = .notDetermined
            calendarEvents = []
            return
        }
        let granted = await calendarService.ensureAuthorized()
        calendarAuthorizationStatus = calendarService.authorizationStatus
        guard granted else {
            calendarEvents = []
            return
        }
        do {
            let events = try await calendarService.upcomingEvents(within: 48)
            calendarEvents = events.compactMap { event in
                guard
                    let start = event.startDate,
                    let end = event.endDate,
                    let id = event.eventIdentifier ?? event.calendarItemIdentifier as String?
                else {
                    return nil
                }
                let cal = event.calendar
                let colorHex: UInt32? = cal?.cgColor.flatMap(Self.hex(from:))
                return CalendarEventSnapshot(
                    id: id,
                    title: event.title ?? "(untitled)",
                    startDate: start,
                    endDate: end,
                    isAllDay: event.isAllDay,
                    calendarTitle: cal?.title ?? "Calendar",
                    calendarColorHex: colorHex,
                    location: event.location,
                    notes: event.notes
                )
            }
        } catch {
            // Don't blow away the rest of the sheet — log via `error` so it
            // shows in the inline banner but the existing actions list keeps
            // rendering.
            self.error = error.localizedDescription
            calendarEvents = []
        }
    }

    /// Snapshots grouped by day, sorted ascending. Used by the sheet to
    /// render "Today" / "Tomorrow" sections.
    var calendarEventsByDay: [(Date, [CalendarEventSnapshot])] {
        let groups = Dictionary(grouping: calendarEvents, by: \.dayKey)
        return groups
            .sorted { $0.key < $1.key }
            .map { ($0.key, $0.value.sorted { $0.startDate < $1.startDate }) }
    }

    private static func hex(from cgColor: CGColor) -> UInt32? {
        guard let comps = cgColor.components, comps.count >= 3 else { return nil }
        let r = UInt32(max(0, min(1, comps[0])) * 255)
        let g = UInt32(max(0, min(1, comps[1])) * 255)
        let b = UInt32(max(0, min(1, comps[2])) * 255)
        return (r << 16) | (g << 8) | b
    }
}
