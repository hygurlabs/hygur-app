import SwiftUI

/// `HygurCard`-based row for email thread lists. Replaces the inline
/// `EmailThreadRow` styling so threads match other entity surfaces.
struct MailCard: View {
    let thread: EmailThread
    var fillContainer: Bool = false

    var body: some View {
        HygurCard(
            icon: "envelope",
            iconTint: HygurColors.brandBlue,
            title: thread.subject,
            subtitle: participantsText,
            fillContainer: fillContainer,
            accessory: {
                if thread.hasAttachments {
                    Image(systemName: "paperclip")
                        .font(.system(size: 12, weight: .medium))
                        .foregroundStyle(HygurColors.textSecondary)
                        .accessibilityHidden(true)
                }
            },
            content: {
                EmptyView()
            },
            footer: {
                HStack(spacing: HygurSpacing.sm) {
                    BadgeView(
                        text: "\(thread.messageCount) messages",
                        color: HygurColors.brandBlue,
                        style: .rounded
                    )
                    Spacer()
                    Text(formattedRange)
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textTertiary)
                }
            }
        )
    }

    private var participantsText: String {
        let head = thread.participants.prefix(3).joined(separator: ", ")
        let extra = thread.participants.count > 3
            ? " +\(thread.participants.count - 3)"
            : ""
        return head + extra
    }

    private var formattedRange: String {
        let end = formatDate(thread.dateEnd)
        guard thread.dateStart != thread.dateEnd else { return end }
        return "\(formatDate(thread.dateStart)) → \(end)"
    }

    private func formatDate(_ raw: String) -> String {
        let iso = ISO8601DateFormatter()
        iso.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let date = iso.date(from: raw) { return Self.short.string(from: date) }
        iso.formatOptions = [.withInternetDateTime]
        if let date = iso.date(from: raw) { return Self.short.string(from: date) }
        return raw
    }

    private static let short: DateFormatter = {
        let f = DateFormatter()
        f.dateStyle = .short
        f.timeStyle = .none
        return f
    }()
}
