import AppIntents
import EventKit
import Foundation

/// "What's on my agenda" — merges real macOS Calendar events (via
/// `CalendarService`) with LLM-extracted deadlines/actions from
/// `SidecarService.agendaContext`. Both feed the same morning / afternoon /
/// deadlines layout so the Siri response is a single time-ordered view.
/// Calendar fetch is best-effort: if access isn't granted we silently fall
/// back to the actions-only summary rather than failing the intent.
struct WhatsOnMyAgendaIntent: AppIntent {
    static let title: LocalizedStringResource = "What's on my agenda"
    static let description = IntentDescription(
        "Get a summary of today's deadlines and actions extracted by Hygur.",
        categoryName: "Hygur"
    )

    /// Read-only intent — no need to open the app.
    static let openAppWhenRun: Bool = false

    static var parameterSummary: some ParameterSummary {
        Summary("What's on my agenda today")
    }

    func perform() async throws -> some IntentResult & ProvidesDialog & ReturnsValue<String> {
        let service = HygurIntentSupport.service()

        let response: AgendaContextResponse
        do {
            response = try await service.agendaContext()
        } catch {
            throw HygurIntentError.operationFailed(error.localizedDescription)
        }

        let events = await Self.fetchTodayEventSnapshots()
        let summary = formatAgenda(actions: response.actions, events: events)
        return .result(value: summary, dialog: IntentDialog(stringLiteral: summary))
    }

    /// Pulls the next 24h of events from EventKit and converts to Sendable
    /// tuples on the main actor so the rest of `perform()` can stay actor-free.
    /// All-day events are excluded — they don't fit the morning/afternoon
    /// model and would dominate the response. Errors / denied access return
    /// an empty list so the intent still produces a useful summary.
    @MainActor
    private static func fetchTodayEventSnapshots() async -> [(start: Date, end: Date, title: String)] {
        let calendarService = CalendarService.shared
        guard await calendarService.ensureAuthorized() else { return [] }
        do {
            let events = try await calendarService.upcomingEvents(within: 24)
            return events.compactMap { event in
                guard
                    !event.isAllDay,
                    let start = event.startDate,
                    let end = event.endDate
                else { return nil }
                return (start, end, event.title ?? "(untitled)")
            }
        } catch {
            return []
        }
    }

    /// Merge calendar events and LLM-extracted actions into morning /
    /// afternoon / deadlines sections, time-ordered within each. Items
    /// without a parseable deadline land in "Deadlines" so they still
    /// surface; items outside today's window do too.
    private func formatAgenda(
        actions: [AgendaAction],
        events: [(start: Date, end: Date, title: String)]
    ) -> String {
        if actions.isEmpty && events.isEmpty {
            return "Nothing on your agenda for today."
        }

        let calendar = Calendar.current
        let now = Date()
        let startOfToday = calendar.startOfDay(for: now)
        guard
            let endOfToday = calendar.date(byAdding: .day, value: 1, to: startOfToday),
            let noon = calendar.date(bySettingHour: 12, minute: 0, second: 0, of: startOfToday)
        else {
            return "Nothing on your agenda for today."
        }

        let isoFormatter = ISO8601DateFormatter()
        isoFormatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let isoFormatterFallback = ISO8601DateFormatter()
        isoFormatterFallback.formatOptions = [.withInternetDateTime]

        let timeFormatter = DateFormatter()
        timeFormatter.locale = Locale.current
        timeFormatter.dateFormat = "HH:mm"

        var morning: [(Date, String)] = []
        var afternoon: [(Date, String)] = []
        var deadlines: [(sortKey: String, label: String)] = []

        for event in events {
            // Calendar fetch is bounded to 24h ahead but events can span
            // across days — gate strictly on today's window for bucketing.
            guard event.start >= startOfToday && event.start < endOfToday else { continue }
            let line = "\(timeFormatter.string(from: event.start)) — \(event.title)"
            if event.start < noon {
                morning.append((event.start, line))
            } else {
                afternoon.append((event.start, line))
            }
        }

        for action in actions {
            let parsed = isoFormatter.date(from: action.deadlineISO)
                ?? isoFormatterFallback.date(from: action.deadlineISO)
            guard let deadline = parsed else {
                deadlines.append((sortKey: action.deadlineISO, label: action.what))
                continue
            }
            guard deadline >= startOfToday && deadline < endOfToday else {
                deadlines.append((sortKey: action.deadlineISO, label: action.what))
                continue
            }
            let line = "\(timeFormatter.string(from: deadline)) — \(action.what)"
            if deadline < noon {
                morning.append((deadline, line))
            } else {
                afternoon.append((deadline, line))
            }
        }

        morning.sort { $0.0 < $1.0 }
        afternoon.sort { $0.0 < $1.0 }
        deadlines.sort { $0.sortKey < $1.sortKey }

        var sections: [String] = []
        if !morning.isEmpty {
            sections.append("Morning:\n" + morning.map { "• \($0.1)" }.joined(separator: "\n"))
        }
        if !afternoon.isEmpty {
            sections.append("Afternoon:\n" + afternoon.map { "• \($0.1)" }.joined(separator: "\n"))
        }
        if !deadlines.isEmpty {
            sections.append("Deadlines:\n" + deadlines.map { "• \($0.label)" }.joined(separator: "\n"))
        }

        if sections.isEmpty {
            return "Nothing on your agenda for today."
        }

        return sections.joined(separator: "\n\n")
    }
}
