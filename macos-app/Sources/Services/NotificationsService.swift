import Foundation
import UserNotifications

/// `NotificationsService` translates sidecar events into native macOS
/// banner notifications. Permission is requested lazily on the first event
/// the user has opted in to receive — never at app launch — so users who
/// don't toggle anything in Settings never see a permission prompt.
///
/// Two opt-in toggles drive the routing: `notify.dailyBrief` and
/// `notify.priorityMail` (read from `UserDefaults`). Everything else is
/// silent (visible only in the in-app Activity view).
@MainActor
final class NotificationsService {
    static let shared = NotificationsService()
    private init() {}

    private let center = UNUserNotificationCenter.current()
    private var hasRequestedAuth = false

    private var dailyBriefEnabled: Bool {
        UserDefaults.standard.bool(forKey: "notify.dailyBrief")
    }

    private var priorityMailEnabled: Bool {
        UserDefaults.standard.bool(forKey: "notify.priorityMail")
    }

    private var agendaAlertsEnabled: Bool {
        // Agenda alerts are opt-in via the same daily brief toggle.
        UserDefaults.standard.bool(forKey: "notify.agendaAlerts")
    }

    /// Hook this on `EventStreamService.onEvent`.
    func handle(_ event: ActivityEvent) {
        switch event.type {
        case "mail_digest" where priorityMailEnabled:
            Task { await postMailDigest(event) }
        case "priority_mail" where priorityMailEnabled:
            // mail_digest aggregates priority_mail items at the end of each
            // sync cycle. To avoid double-notifying, individual priority_mail
            // events are now logged in ActivityView only — the digest is the
            // user-facing notification path.
            return
        case "brief" where dailyBriefEnabled:
            Task { await postDailyBrief(event) }
        case "agenda_alert":
            Task { await postAgendaAlert(event) }
        case "meeting_briefing":
            Task { await postMeetingBriefing(event) }
        default:
            return
        }
    }

    /// Settings toggles call this when the user enables either notification
    /// category. Idempotent — repeated calls don't re-prompt the user.
    func ensureAuthorization() async {
        if hasRequestedAuth { return }
        hasRequestedAuth = true
        do {
            _ = try await center.requestAuthorization(options: [.alert, .sound, .badge])
        } catch {
            // Silent failure — user will see no notifications, but the rest
            // of the app keeps working. The Activity view is the always-on
            // fallback channel.
        }
    }

    /// Posts a notification on demand — used by the WebUI bridge
    /// (`HygurNative.notify`). Ensures authorization first; intentionally
    /// ungated by the opt-in toggles since the caller explicitly asked.
    func postDirect(title: String, body: String) async {
        await ensureAuthorization()
        let content = UNMutableNotificationContent()
        content.title = title
        content.body = body
        content.sound = .default
        content.userInfo = ["kind": "webui"]
        let req = UNNotificationRequest(
            identifier: "webui-\(UUID().uuidString)",
            content: content,
            trigger: nil
        )
        try? await center.add(req)
    }

    // MARK: - Posting

    /// Renders a `mail_digest` event into a macOS notification. The plan
    /// dictates three layouts depending on the count:
    ///   - 1 mail  → single-line body with the one_liner.
    ///   - 2-3     → grouped, body lists every one_liner on its own row.
    ///   - 4+      → grouped, body shows top-3 one_liners + "+N more".
    /// Tap routes to the Activity view via `userInfo.kind` so the existing
    /// notification-tap pipeline can deep-link.
    private func postMailDigest(_ event: ActivityEvent) async {
        let items = event.raw.digestItems() ?? []
        if items.isEmpty { return }
        let total = event.raw.int("count") ?? items.count

        let content = UNMutableNotificationContent()
        content.sound = .default

        if items.count == 1, let only = items.first {
            content.title = "Important email"
            content.body = only.oneLiner
        } else if items.count <= 3 {
            content.title = "\(items.count) new important emails"
            content.body = items.map(\.oneLiner).joined(separator: "\n")
        } else {
            content.title = "\(total) new important emails"
            let top = items.prefix(3).map(\.oneLiner).joined(separator: "\n")
            let extra = total - 3
            content.body = extra > 0 ? "\(top)\n+\(extra) more" : top
        }

        content.userInfo = [
            "kind": "mail_digest",
            "count": total,
            "content_ids": items.map(\.contentId),
        ]

        let req = UNNotificationRequest(identifier: "mail-digest-\(event.id.uuidString)", content: content, trigger: nil)
        try? await center.add(req)
    }

    private func postPriorityMail(_ event: ActivityEvent) async {
        let content = UNMutableNotificationContent()
        let from = event.raw.string("from") ?? "Unknown sender"
        content.title = "Important email — \(from)"

        var bodyParts: [String] = []
        if let title = event.raw.string("title"), !title.isEmpty { bodyParts.append(title) }
        if let amount = event.raw.string("amount"), !amount.isEmpty { bodyParts.append(amount) }
        if let due = event.raw.string("due_date"), !due.isEmpty { bodyParts.append("due \(due)") }
        content.body = bodyParts.joined(separator: " · ")
        content.sound = .default
        content.userInfo = [
            "kind": "priority_mail",
            "content_id": event.raw.string("content_id") ?? event.source,
        ]

        let req = UNNotificationRequest(identifier: "priority-\(event.id.uuidString)", content: content, trigger: nil)
        try? await center.add(req)
    }

    private func postAgendaAlert(_ event: ActivityEvent) async {
        let what = event.raw.string("what") ?? event.message ?? "Upcoming action"
        let deadline = event.raw.string("deadline_iso") ?? ""

        let content = UNMutableNotificationContent()
        content.title = "Upcoming deadline: \(what)"
        content.body = deadline.isEmpty ? "" : "Deadline: \(relativeDeadlineLabel(from: deadline))"
        content.sound = .default
        content.userInfo = [
            "kind": "agenda_alert",
            "source_id": event.raw.string("source_id") ?? event.source,
            "deadline_iso": deadline,
        ]

        let req = UNNotificationRequest(
            identifier: "agenda-alert-\(event.id.uuidString)",
            content: content,
            trigger: nil
        )
        try? await center.add(req)
    }

    /// Converts an ISO date string (YYYY-MM-DD) to a human-readable relative
    /// label. Uses a factory function to comply with Swift 6 Sendable rules —
    /// DateFormatter is not Sendable, so a static let would violate isolation.
    private func relativeDeadlineLabel(from isoDate: String) -> String {
        let formatter = notifDateFormatter()
        guard let date = formatter.date(from: isoDate) else { return isoDate }
        let days = Calendar.current.dateComponents([.day], from: Date(), to: date).day ?? 0
        if days <= 0 { return "Today" }
        if days == 1 { return "Tomorrow" }
        return "In \(days) days"
    }

    private func notifDateFormatter() -> DateFormatter {
        let f = DateFormatter()
        f.dateFormat = "yyyy-MM-dd"
        f.locale = Locale(identifier: "en_US_POSIX")
        f.timeZone = TimeZone(identifier: "UTC")
        return f
    }

    /// Renders a `meeting_briefing` event (calendar event or mail deadline) into
    /// a notification. Self-authorizes (like `postDirect`) so it works even if
    /// the user never toggled the other notification categories — the sidecar
    /// only emits this when it found relevant context, so it's not noisy.
    private func postMeetingBriefing(_ event: ActivityEvent) async {
        await ensureAuthorization()
        let title = event.raw.string("title") ?? event.message ?? "Upcoming"
        let content = UNMutableNotificationContent()
        content.title = "Briefing — \(title)"
        if let bullets = event.raw.stringArray("bullets"), !bullets.isEmpty {
            content.body = bullets.prefix(2).joined(separator: " · ")
        } else {
            content.body = "Préparation avant échéance."
        }
        content.sound = .default
        content.userInfo = [
            "kind": "meeting_briefing",
            "content_id": event.raw.string("content_id") ?? event.source,
        ]
        let req = UNNotificationRequest(
            identifier: "meeting-brief-\(event.id.uuidString)",
            content: content,
            trigger: nil
        )
        try? await center.add(req)
    }

    private func postDailyBrief(_ event: ActivityEvent) async {
        let content = UNMutableNotificationContent()
        let date = event.raw.string("date") ?? ""
        content.title = date.isEmpty ? "Daily brief" : "Brief — \(date)"

        if let bullets = event.raw.stringArray("bullets"), !bullets.isEmpty {
            content.body = bullets.prefix(2).joined(separator: " · ")
        } else if let count = event.raw.int("item_count"), count > 0 {
            content.body = "\(count) activity items summarized"
        } else {
            content.body = "No activity in the last 24 hours."
        }
        content.sound = .default
        content.userInfo = [
            "kind": "brief",
            "content_id": event.raw.string("content_id") ?? event.source,
        ]

        let req = UNNotificationRequest(identifier: "brief-\(event.id.uuidString)", content: content, trigger: nil)
        try? await center.add(req)
    }
}
