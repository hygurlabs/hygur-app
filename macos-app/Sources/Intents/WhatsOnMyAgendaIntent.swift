import AppIntents
import Foundation

/// "What's on my agenda" — summarises today's deadlines/actions extracted
/// from recently indexed knowledge items. Reuses `SidecarService.agendaContext`,
/// the same call backing `AgendaViewModel.refresh()`. If a `CalendarService`
/// is added later (Phase 2.1), this intent should also fetch upcoming events
/// and merge them into the morning/afternoon sections — see comment below.
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

        // CalendarService isn't wired up yet (Phase 2.1 in flight). When
        // it lands, fetch today's events here and prepend them to the
        // summary — order: morning events, afternoon events, then deadlines.
        let summary = formatAgenda(actions: response.actions)
        return .result(value: summary, dialog: IntentDialog(stringLiteral: summary))
    }

    /// Bucket actions into morning / afternoon / unscheduled and emit a
    /// readable summary. Items without a parseable deadline land in
    /// "Deadlines" so they still surface.
    private func formatAgenda(actions: [AgendaAction]) -> String {
        guard !actions.isEmpty else {
            return "Nothing on your agenda for today."
        }

        let calendar = Calendar.current
        let now = Date()
        let startOfToday = calendar.startOfDay(for: now)
        guard let endOfToday = calendar.date(byAdding: .day, value: 1, to: startOfToday) else {
            return "Nothing on your agenda for today."
        }
        guard let noon = calendar.date(bySettingHour: 12, minute: 0, second: 0, of: startOfToday) else {
            return "Nothing on your agenda for today."
        }

        let isoFormatter = ISO8601DateFormatter()
        isoFormatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let isoFormatterFallback = ISO8601DateFormatter()
        isoFormatterFallback.formatOptions = [.withInternetDateTime]

        let timeFormatter = DateFormatter()
        timeFormatter.locale = Locale.current
        timeFormatter.dateFormat = "HH:mm"

        var morning: [(Date, AgendaAction)] = []
        var afternoon: [(Date, AgendaAction)] = []
        var deadlines: [AgendaAction] = []

        for action in actions {
            let parsed = isoFormatter.date(from: action.deadlineISO)
                ?? isoFormatterFallback.date(from: action.deadlineISO)
            guard let deadline = parsed else {
                deadlines.append(action)
                continue
            }
            // Anything outside today still lands in "Deadlines" so the
            // user sees it without dropping context.
            guard deadline >= startOfToday && deadline < endOfToday else {
                deadlines.append(action)
                continue
            }
            if deadline < noon {
                morning.append((deadline, action))
            } else {
                afternoon.append((deadline, action))
            }
        }

        morning.sort { $0.0 < $1.0 }
        afternoon.sort { $0.0 < $1.0 }
        deadlines.sort { $0.deadlineISO < $1.deadlineISO }

        var sections: [String] = []
        if !morning.isEmpty {
            let lines = morning.map { "• \(timeFormatter.string(from: $0.0)) — \($0.1.what)" }
            sections.append("Morning:\n" + lines.joined(separator: "\n"))
        }
        if !afternoon.isEmpty {
            let lines = afternoon.map { "• \(timeFormatter.string(from: $0.0)) — \($0.1.what)" }
            sections.append("Afternoon:\n" + lines.joined(separator: "\n"))
        }
        if !deadlines.isEmpty {
            let lines = deadlines.map { "• \($0.what)" }
            sections.append("Deadlines:\n" + lines.joined(separator: "\n"))
        }

        if sections.isEmpty {
            return "Nothing on your agenda for today."
        }

        return sections.joined(separator: "\n\n")
    }
}
