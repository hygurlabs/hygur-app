import Foundation

/// Wire types for POST /brief/meeting.
struct MeetingBriefRequest: Encodable {
    let eventId: String
    let title: String
    let attendees: [String]
    let notes: String
    let location: String
    let start: String // RFC3339

    enum CodingKeys: String, CodingKey {
        case eventId = "event_id"
        case title, attendees, notes, location, start
    }
}

struct MeetingBriefResponse: Decodable {
    let relevant: Bool
    let contentId: String?
    let bullets: [String]?

    enum CodingKeys: String, CodingKey {
        case relevant
        case contentId = "content_id"
        case bullets
    }
}

/// Drives the calendar half of the meeting-briefing feature: a polling timer
/// that, ~30 min before each upcoming event in the user-selected calendars,
/// asks the sidecar to generate a RAG briefing (which then emits a
/// `meeting_briefing` SSE event the NotificationsService turns into a banner).
///
/// EventKit lives only on the native side, so the timing decision is made here.
/// The mail-deadline half runs server-side in the sidecar's scheduler.
///
/// Gated by two UserDefaults keys written by the WebUI calendar bridge:
///   - `calendar.briefing.enabled` (Bool)
///   - `calendar.briefing.calendars` ([String] of EKCalendar identifiers)
@MainActor
final class MeetingBriefingScheduler {
    static let shared = MeetingBriefingScheduler()
    private init() {}

    /// Brief this many seconds before an event starts.
    private let leadWindow: TimeInterval = 30 * 60
    /// How often the timer wakes to check for imminent events.
    private let pollInterval: UInt64 = 3 * 60 * 1_000_000_000 // 3 min

    private var task: Task<Void, Never>?
    /// Events already briefed this run, keyed by calendar+start+title, so each
    /// event fires at most once per app session.
    private var briefed: Set<String> = []

    func start() {
        guard task == nil else { return }
        task = Task { [weak self] in
            while !Task.isCancelled {
                await self?.tick()
                try? await Task.sleep(nanoseconds: self?.pollInterval ?? 180_000_000_000)
            }
        }
    }

    func stop() {
        task?.cancel()
        task = nil
    }

    private func tick() async {
        guard UserDefaults.standard.bool(forKey: "calendar.briefing.enabled") else { return }
        guard CalendarService.shared.hasFullAccess else { return }

        let calendarIDs = UserDefaults.standard.stringArray(forKey: "calendar.briefing.calendars") ?? []
        let events: [EventSnapshot]
        do {
            // One hour of lookahead is plenty for a 30-min lead at a 3-min poll.
            events = try await CalendarService.shared.eventSnapshots(within: 1, calendarIDs: calendarIDs)
        } catch {
            return
        }

        let now = Date()
        let iso = ISO8601DateFormatter()
        for ev in events where !ev.allDay {
            guard let start = iso.date(from: ev.start) else { continue }
            let fireAt = start.addingTimeInterval(-leadWindow)
            // Within the lead window and the event hasn't started yet.
            guard now >= fireAt, now < start else { continue }
            let key = ev.calendarId + "|" + ev.start + "|" + ev.title
            if briefed.contains(key) { continue }
            briefed.insert(key)
            await requestBrief(ev, start: start)
        }

        // Bound memory across a long-running session.
        if briefed.count > 500 { briefed.removeAll() }
    }

    private func requestBrief(_ ev: EventSnapshot, start: Date) async {
        do {
            _ = try await SidecarService.fromSettings().meetingBrief(
                eventID: ev.calendarId + ":" + ev.start,
                title: ev.title,
                attendees: ev.attendees,
                notes: ev.notes,
                location: ev.location,
                start: start
            )
            // The notification (if relevant) is emitted by the sidecar via SSE
            // → NotificationsService.handle("meeting_briefing").
        } catch {
            // Best-effort: a failed brief just means no notification this time.
        }
    }
}
