import SwiftUI

/// Modal listing the urgent actions extracted from the user's documents
/// (the "focus mode" surfaced in chat). Presented as a sheet, but caller
/// closes it through the explicit close button or Esc — macOS sheets do
/// not dismiss on outside-click, so we make sure both paths actually work.
struct AgendaSheet: View {
    let actions: [AgendaAction]

    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            content
        }
        .frame(minWidth: 600, idealWidth: 640, minHeight: 520, idealHeight: 560)
    }

    // MARK: - Header

    private var header: some View {
        HStack(spacing: HygurSpacing.md) {
            VStack(alignment: .leading, spacing: 2) {
                Text("Urgent actions")
                    .font(.title3)
                    .fontWeight(.semibold)
                Text("\(actions.count) action\(actions.count > 1 ? "s" : "") in the next 48 hours")
                    .font(.caption)
                    .foregroundStyle(HygurColors.textSecondary)
            }
            Spacer()
            Button {
                dismiss()
            } label: {
                Image(systemName: "xmark.circle.fill")
                    .font(.title2)
                    .foregroundStyle(HygurColors.textSecondary)
            }
            .buttonStyle(.plain)
            .keyboardShortcut(.escape, modifiers: [])
            .help("Close (Esc)")
        }
        .padding(HygurSpacing.lg)
    }

    // MARK: - Content

    @ViewBuilder
    private var content: some View {
        if actions.isEmpty {
            ContentUnavailableView(
                "No upcoming actions",
                systemImage: "checkmark.circle",
                description: Text("No deadlines in the next 48 hours.")
            )
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            ScrollView {
                LazyVStack(spacing: HygurSpacing.sm) {
                    ForEach(actions) { action in
                        AgendaActionRow(action: action) {
                            // Open the source document via the central
                            // notification so KnowledgeBaseView can present
                            // QuickLook regardless of which view is active.
                            NotificationCenter.default.post(
                                name: .navigateToSection,
                                object: "knowledgeBase"
                            )
                            NotificationCenter.default.post(
                                name: .openDocument,
                                object: action.sourceId
                            )
                            dismiss()
                        }
                    }
                }
                .padding(HygurSpacing.md)
            }
        }
    }
}

// MARK: - Action Row

private struct AgendaActionRow: View {
    let action: AgendaAction
    let onOpen: () -> Void

    @State private var isHovered = false
    @State private var isLinkHovered = false

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: HygurSpacing.sm) {
                PriorityPill(priority: action.priority)
                Text(action.what)
                    .font(.body)
                    .fontWeight(.medium)
                    .lineLimit(2)
                Spacer()
            }
            HStack(spacing: HygurSpacing.md) {
                Label(relativeDeadline(from: action.deadlineISO), systemImage: "calendar")
                    .font(.caption)
                    .foregroundStyle(HygurColors.textSecondary)

                Spacer()

                Button("View document") {
                    onOpen()
                }
                .font(.caption)
                .buttonStyle(.plain)
                .foregroundStyle(isLinkHovered ? Color.accentColor : .blue)
                .underline(isLinkHovered, color: Color.accentColor)
                .onHover { hovering in
                    isLinkHovered = hovering
                    if hovering {
                        NSCursor.pointingHand.push()
                    } else {
                        NSCursor.pop()
                    }
                }
            }
        }
        .padding(HygurSpacing.md)
        .background(
            RoundedRectangle(cornerRadius: HygurRadius.md)
                .fill(isHovered ? HygurColors.accent.opacity(0.06) : HygurColors.surfaceElevated.opacity(0.4))
        )
        .overlay(
            RoundedRectangle(cornerRadius: HygurRadius.md)
                .strokeBorder(
                    isHovered ? HygurColors.accent.opacity(0.30) : HygurColors.border.opacity(0.6),
                    lineWidth: 0.5
                )
        )
        .contentShape(Rectangle())
        .onHover { hovering in
            isHovered = hovering
        }
        .onTapGesture(count: 2) {
            onOpen()
        }
        .accessibilityHint("Double-click to open the source document")
    }

    private func relativeDeadline(from isoDate: String) -> String {
        let formatter = agendaDateFormatter()
        guard let date = formatter.date(from: isoDate) else { return isoDate }
        return relativeString(for: date)
    }

    private func relativeString(for date: Date) -> String {
        let now = Date()
        let components = Calendar.current.dateComponents([.hour, .day], from: now, to: date)
        let days = components.day ?? 0
        let hours = components.hour ?? 0

        if hours <= 0 && days <= 0 {
            return "Past"
        } else if days == 0 {
            return "in \(hours)h"
        } else if days == 1 {
            return "tomorrow"
        } else {
            return "in \(days) days"
        }
    }
}

// MARK: - Priority Pill

private struct PriorityPill: View {
    let priority: String

    private var backgroundColor: Color {
        switch priority {
        case "high":   return .red.opacity(0.15)
        case "medium": return .orange.opacity(0.15)
        default:       return Color(.systemGray).opacity(0.15)
        }
    }

    private var foregroundColor: Color {
        switch priority {
        case "high":   return .red
        case "medium": return .orange
        default:       return .secondary
        }
    }

    private var label: String {
        switch priority {
        case "high":   return "Urgent"
        case "medium": return "Medium"
        default:       return "Low"
        }
    }

    var body: some View {
        Text(label)
            .font(.caption2)
            .fontWeight(.semibold)
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(backgroundColor, in: Capsule())
            .foregroundStyle(foregroundColor)
    }
}

// MARK: - Date Formatter Factory (Swift 6: no static let on non-Sendable types)

private func agendaDateFormatter() -> DateFormatter {
    let f = DateFormatter()
    f.dateFormat = "yyyy-MM-dd"
    f.locale = Locale(identifier: "en_US_POSIX")
    f.timeZone = TimeZone(identifier: "UTC")
    return f
}

#Preview {
    AgendaSheet(actions: [
        AgendaAction(what: "Send Q2 report", deadlineISO: "2026-05-06", priority: "high", sourceId: "doc-1", confidence: 1.0),
        AgendaAction(what: "Review contract", deadlineISO: "2026-05-07", priority: "medium", sourceId: "doc-2", confidence: 0.9),
    ])
}
