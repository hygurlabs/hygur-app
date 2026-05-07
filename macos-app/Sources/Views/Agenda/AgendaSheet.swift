import SwiftUI
import AppKit
import EventKit

/// Modal listing the urgent actions extracted from the user's documents
/// (the "focus mode" surfaced in chat) plus the next 48h of macOS Calendar
/// events. Presented as a sheet, but caller closes it through the explicit
/// close button or Esc — macOS sheets do not dismiss on outside-click, so we
/// make sure both paths actually work.
struct AgendaSheet: View {
    /// Bound to the parent's `AgendaViewModel` so the sheet can drive its own
    /// EventKit refresh on appear without forcing the parent to know about
    /// calendar permissions. Existing call sites that only had `actions` keep
    /// working through the convenience initialiser below.
    @Bindable var viewModel: AgendaViewModel

    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            content
        }
        .frame(minWidth: 600, idealWidth: 640, minHeight: 520, idealHeight: 600)
        .task {
            // Refresh calendar on every present so the user sees up-to-date
            // events even if Calendar.app changed between sheet opens.
            await viewModel.refreshCalendar()
        }
    }

    // MARK: - Header

    private var header: some View {
        HStack(spacing: HygurSpacing.md) {
            VStack(alignment: .leading, spacing: 2) {
                Text("Agenda")
                    .font(.title3)
                    .fontWeight(.semibold)
                Text(headerSubtitle)
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

    private var headerSubtitle: String {
        let actionCount = viewModel.actions.count
        let eventCount = viewModel.calendarEvents.count
        let actionPart = "\(actionCount) action\(actionCount == 1 ? "" : "s")"
        let eventPart = "\(eventCount) event\(eventCount == 1 ? "" : "s")"
        return "\(actionPart) · \(eventPart) in the next 48 hours"
    }

    // MARK: - Content

    @ViewBuilder
    private var content: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: HygurSpacing.lg) {
                calendarSection
                actionsSection
            }
            .padding(HygurSpacing.md)
        }
    }

    // MARK: - Calendar section

    @ViewBuilder
    private var calendarSection: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            HStack {
                Text("Calendar")
                    .font(HygurTypography.headline)
                    .foregroundStyle(HygurColors.textPrimary)
                Spacer()
            }
            .padding(.horizontal, HygurSpacing.xs)

            switch viewModel.calendarAuthorizationStatus {
            case .denied, .restricted:
                permissionCTA
            case .notDetermined:
                // The view model triggers the prompt on .task; while waiting
                // we show a compact placeholder rather than flashing empty.
                ProgressView()
                    .controlSize(.small)
                    .padding(.vertical, HygurSpacing.sm)
                    .frame(maxWidth: .infinity, alignment: .center)
            default:
                calendarEventList
            }
        }
    }

    private var permissionCTA: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            HStack(spacing: HygurSpacing.sm) {
                Image(systemName: "calendar.badge.exclamationmark")
                    .foregroundStyle(HygurColors.warning)
                VStack(alignment: .leading, spacing: 2) {
                    Text("Calendar access not granted")
                        .font(HygurTypography.subheadline.weight(.semibold))
                    Text("Hygur can show your upcoming events here once you enable Calendar access.")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                }
                Spacer()
            }
            HStack {
                Spacer()
                Button("Open Privacy Settings") {
                    if let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_Calendars") {
                        NSWorkspace.shared.open(url)
                    }
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
            }
        }
        .padding(HygurSpacing.md)
        .background(
            RoundedRectangle(cornerRadius: HygurRadius.md)
                .fill(HygurColors.warning.opacity(0.10))
        )
        .overlay(
            RoundedRectangle(cornerRadius: HygurRadius.md)
                .strokeBorder(HygurColors.warning.opacity(0.30), lineWidth: 0.5)
        )
    }

    @ViewBuilder
    private var calendarEventList: some View {
        if viewModel.calendarEvents.isEmpty {
            HStack {
                Image(systemName: "calendar")
                    .foregroundStyle(HygurColors.textTertiary)
                Text("No events in the next 48 hours")
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textSecondary)
                Spacer()
            }
            .padding(HygurSpacing.md)
            .background(
                RoundedRectangle(cornerRadius: HygurRadius.md)
                    .fill(HygurColors.surfaceElevated.opacity(0.4))
            )
        } else {
            VStack(alignment: .leading, spacing: HygurSpacing.md) {
                ForEach(viewModel.calendarEventsByDay, id: \.0) { day, events in
                    VStack(alignment: .leading, spacing: HygurSpacing.xs) {
                        Text(dayLabel(for: day))
                            .font(HygurTypography.caption)
                            .fontWeight(.semibold)
                            .foregroundStyle(HygurColors.textTertiary)
                            .textCase(.uppercase)
                            .padding(.horizontal, HygurSpacing.xs)
                        VStack(spacing: HygurSpacing.xs) {
                            ForEach(events) { event in
                                CalendarEventRow(event: event)
                            }
                        }
                    }
                }
            }
        }
    }

    private func dayLabel(for date: Date) -> String {
        let cal = Calendar.current
        if cal.isDateInToday(date) { return "Today" }
        if cal.isDateInTomorrow(date) { return "Tomorrow" }
        let formatter = DateFormatter()
        formatter.dateStyle = .full
        formatter.timeStyle = .none
        return formatter.string(from: date)
    }

    // MARK: - Actions section

    @ViewBuilder
    private var actionsSection: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            HStack {
                Text("Urgent actions")
                    .font(HygurTypography.headline)
                    .foregroundStyle(HygurColors.textPrimary)
                Spacer()
                Text("\(viewModel.actions.count)")
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textSecondary)
            }
            .padding(.horizontal, HygurSpacing.xs)

            if viewModel.actions.isEmpty {
                HStack {
                    Image(systemName: "checkmark.circle")
                        .foregroundStyle(HygurColors.success)
                    Text("No deadlines in the next 48 hours.")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                    Spacer()
                }
                .padding(HygurSpacing.md)
                .background(
                    RoundedRectangle(cornerRadius: HygurRadius.md)
                        .fill(HygurColors.surfaceElevated.opacity(0.4))
                )
            } else {
                LazyVStack(spacing: HygurSpacing.sm) {
                    ForEach(viewModel.actions) { action in
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
            }
        }
    }
}

// MARK: - Calendar Event Row

private struct CalendarEventRow: View {
    let event: CalendarEventSnapshot

    private var calendarTint: Color {
        if let hex = event.calendarColorHex {
            return Color.dynamic(lightHex: hex, darkHex: hex)
        }
        return HygurColors.accent
    }

    var body: some View {
        HStack(alignment: .top, spacing: HygurSpacing.md) {
            // Vertical color rail mirroring Calendar.app — anchors the row
            // to its source calendar without fighting the rest of the layout.
            RoundedRectangle(cornerRadius: 2)
                .fill(calendarTint)
                .frame(width: 3)

            VStack(alignment: .leading, spacing: 2) {
                Text(event.title)
                    .font(HygurTypography.subheadline.weight(.semibold))
                    .foregroundStyle(HygurColors.textPrimary)
                    .lineLimit(2)
                HStack(spacing: HygurSpacing.sm) {
                    Image(systemName: "clock")
                        .font(.caption2)
                        .foregroundStyle(HygurColors.textTertiary)
                    Text(timeLabel)
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                    if let location = event.location, !location.isEmpty {
                        Image(systemName: "mappin")
                            .font(.caption2)
                            .foregroundStyle(HygurColors.textTertiary)
                        Text(location)
                            .font(HygurTypography.caption)
                            .foregroundStyle(HygurColors.textSecondary)
                            .lineLimit(1)
                    }
                }
                Text(event.calendarTitle)
                    .font(.caption2)
                    .foregroundStyle(HygurColors.textTertiary)
            }
            Spacer()
        }
        .padding(HygurSpacing.md)
        .background(
            RoundedRectangle(cornerRadius: HygurRadius.md)
                .fill(HygurColors.surfaceElevated.opacity(0.4))
        )
        .overlay(
            RoundedRectangle(cornerRadius: HygurRadius.md)
                .strokeBorder(HygurColors.border.opacity(0.6), lineWidth: 0.5)
        )
    }

    private var timeLabel: String {
        if event.isAllDay { return "All day" }
        let formatter = DateFormatter()
        formatter.dateStyle = .none
        formatter.timeStyle = .short
        return "\(formatter.string(from: event.startDate)) – \(formatter.string(from: event.endDate))"
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
    let vm = AgendaViewModel()
    vm.actions = [
        AgendaAction(what: "Send Q2 report", deadlineISO: "2026-05-06", priority: "high", sourceId: "doc-1", confidence: 1.0),
        AgendaAction(what: "Review contract", deadlineISO: "2026-05-07", priority: "medium", sourceId: "doc-2", confidence: 0.9),
    ]
    return AgendaSheet(viewModel: vm)
}
