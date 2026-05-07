import SwiftUI

/// `ActivityView` shows the chronological log of sidecar events:
/// daily briefs, priority emails, connector syncs, ingestion completion,
/// and LM Studio status flips. Acts as the always-on counterpart to the
/// macOS notifications layer (which the user can opt out of).
struct ActivityView: View {
    @Environment(EventStreamService.self) private var events
    @Environment(\.openURL) private var openURL

    @State private var typeFilter: TypeFilter = .all
    @State private var briefSheet: BriefSheetTarget?

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Divider()
            content
        }
        .frame(minWidth: 480)
        .sheet(item: $briefSheet) { target in
            BriefDetailView(contentId: target.contentId, fallbackTitle: target.title)
        }
    }

    private struct BriefSheetTarget: Identifiable {
        let contentId: String
        let title: String
        var id: String { contentId }
    }

    private var header: some View {
        HStack(alignment: .center) {
            Image(systemName: "bell.badge")
                .font(.title2)
                .foregroundStyle(.tint)
            VStack(alignment: .leading, spacing: 2) {
                Text("Activity")
                    .font(.title2).bold()
                Text("Recent sidecar events")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Picker("", selection: $typeFilter) {
                ForEach(TypeFilter.allCases, id: \.self) { f in
                    Text(f.label).tag(f)
                }
            }
            .pickerStyle(.segmented)
            .frame(maxWidth: 360)
        }
        .padding()
    }

    @ViewBuilder
    private var content: some View {
        let filtered = events.recentEvents.filter(typeFilter.matches)
        if filtered.isEmpty {
            VStack(spacing: 8) {
                Image(systemName: "tray")
                    .font(.largeTitle)
                    .foregroundStyle(.secondary)
                Text("No activity yet")
                    .font(.title3)
                Text("Events from background syncs, briefs, and priority emails will appear here.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .padding()
        } else {
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(grouped(filtered), id: \.day) { section in
                        Text(section.day)
                            .font(.caption.smallCaps())
                            .foregroundStyle(.secondary)
                            .padding(.horizontal, 16)
                            .padding(.top, 12)
                        ForEach(section.events) { event in
                            ActivityRow(event: event, onOpen: { handleOpen(event) })
                            Divider()
                        }
                    }
                }
            }
        }
    }

    private struct Section {
        let day: String
        let events: [ActivityEvent]
    }

    /// Routes a row tap. Currently only `brief` events have a destination —
    /// other types remain informational.
    private func handleOpen(_ event: ActivityEvent) {
        guard event.type == "brief" else { return }
        // The sidecar publishes the persisted item's id under `content_id` in
        // the event payload (see scheduler/daily_brief.go); fall back to
        // `event.source` which carries the same value for legacy payloads.
        let cid = event.raw.string("content_id") ?? event.source
        guard !cid.isEmpty else { return }
        briefSheet = BriefSheetTarget(contentId: cid, title: event.title)
    }

    private func grouped(_ list: [ActivityEvent]) -> [Section] {
        let calendar = Calendar.current
        let formatter = DateFormatter()
        formatter.dateStyle = .medium
        formatter.locale = .current

        var sections: [Section] = []
        var current: (day: String, items: [ActivityEvent])?
        for event in list {
            let day = calendar.isDateInToday(event.receivedAt) ? "Today"
                : calendar.isDateInYesterday(event.receivedAt) ? "Yesterday"
                : formatter.string(from: event.receivedAt)
            if current?.day == day {
                current?.items.append(event)
            } else {
                if let c = current { sections.append(.init(day: c.day, events: c.items)) }
                current = (day, [event])
            }
        }
        if let c = current { sections.append(.init(day: c.day, events: c.items)) }
        return sections
    }

    enum TypeFilter: CaseIterable, Hashable {
        case all, briefs, priorityMail, connectors, system

        var label: String {
            switch self {
            case .all: return "All"
            case .briefs: return "Briefs"
            case .priorityMail: return "Priority"
            case .connectors: return "Sync"
            case .system: return "System"
            }
        }

        func matches(_ e: ActivityEvent) -> Bool {
            switch self {
            case .all: return true
            case .briefs: return e.type == "brief"
            case .priorityMail: return e.type == "priority_mail" || e.type == "mail_digest"
            case .connectors:
                return e.type == "connectors" || e.type == "ingest" || e.type == "mail"
                    || e.type == "ingest_start" || e.type == "ingest_progress" || e.type == "ingest_complete"
            case .system: return e.type == "lm_studio" || e.type == "sync"
            }
        }
    }
}

private struct ActivityRow: View {
    let event: ActivityEvent
    let onOpen: () -> Void

    private var isOpenable: Bool {
        event.type == "brief"
    }

    var body: some View {
        Button(action: onOpen) {
            HStack(alignment: .top, spacing: 12) {
                Image(systemName: iconName)
                    .foregroundStyle(iconColor)
                    .frame(width: 24)
                    .padding(.top, 2)

                VStack(alignment: .leading, spacing: 4) {
                    Text(event.title)
                        .font(.body)
                        .lineLimit(2)
                    if let detail = detailText {
                        Text(detail)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(2)
                    }
                }
                Spacer()
                if isOpenable {
                    Image(systemName: "chevron.right")
                        .font(.caption)
                        .foregroundStyle(.tertiary)
                }
                RelativeTimeText(date: event.receivedAt)
                    .font(.caption)
                    .foregroundStyle(.tertiary)
                    .padding(.top, 2)
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(!isOpenable)
    }

    private var iconName: String {
        switch event.type {
        case "brief": return "doc.text.below.ecg"
        case "priority_mail": return "envelope.badge.fill"
        case "mail_digest": return "tray.full.fill"
        case "lm_studio": return "cpu"
        case "connectors": return "puzzlepiece.extension"
        case "ingest", "ingest_start", "ingest_progress": return "tray.and.arrow.down"
        case "ingest_complete": return "tray.and.arrow.down.fill"
        case "mail": return "envelope"
        case "sync": return "arrow.triangle.2.circlepath"
        case "sidecar_restart": return "arrow.clockwise.circle.fill"
        case "chat_failed": return "exclamationmark.bubble.fill"
        case "embedding_failed": return "waveform.slash"
        default: return "circle.fill"
        }
    }

    private var iconColor: Color {
        switch event.status {
        case "completed": return HygurColors.success
        case "failed": return HygurColors.danger
        case "running", "pending": return HygurColors.warning
        default: return .secondary
        }
    }

    private var detailText: String? {
        switch event.type {
        case "priority_mail":
            var parts: [String] = []
            if let from = event.raw.string("from"), !from.isEmpty { parts.append(from) }
            if let amount = event.raw.string("amount"), !amount.isEmpty { parts.append(amount) }
            if let due = event.raw.string("due_date"), !due.isEmpty { parts.append("due " + due) }
            return parts.isEmpty ? event.message : parts.joined(separator: " · ")
        case "mail_digest":
            // Show the one_liners themselves, not just a count — they're
            // already short and pre-formatted with emoji prefixes.
            if let items = event.raw.digestItems(), !items.isEmpty {
                return items.prefix(3).map(\.oneLiner).joined(separator: "\n")
            }
            return event.message
        case "brief":
            if let count = event.raw.int("item_count") {
                return "\(count) items summarised"
            }
            return event.message
        case "lm_studio":
            if let url = event.raw.string("url"), !url.isEmpty {
                return url
            }
            return event.message
        case "ingest_complete":
            if let ms = event.raw.int("duration_ms") {
                return "\(ms) ms"
            }
            return event.message
        default:
            return event.message
        }
    }
}
