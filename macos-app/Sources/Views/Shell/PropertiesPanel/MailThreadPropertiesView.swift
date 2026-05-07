import SwiftUI

/// Right-rail inspector for a mail thread. Shows the metadata already on
/// the EmailThread payload — no extra fetch needed since the row passes
/// the full thread up via InspectorSelection.
struct MailThreadPropertiesView: View {
    let thread: EmailThread

    var body: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.md) {
            row(label: "Subject", value: thread.subject)
            row(label: "Messages", value: "\(thread.messageCount)")
            row(label: "Attachments", value: thread.hasAttachments ? "Yes" : "No")
            row(label: "First", value: formatDate(thread.dateStart))
            row(label: "Last", value: formatDate(thread.dateEnd))

            if !thread.participants.isEmpty {
                VStack(alignment: .leading, spacing: HygurSpacing.xs) {
                    Text("Participants")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                    ForEach(thread.participants.prefix(8), id: \.self) { person in
                        Text(person)
                            .font(HygurTypography.cardMeta)
                            .foregroundStyle(HygurColors.textPrimary)
                            .textSelection(.enabled)
                            .lineLimit(1)
                    }
                    if thread.participants.count > 8 {
                        Text("+\(thread.participants.count - 8) more")
                            .font(HygurTypography.caption)
                            .foregroundStyle(HygurColors.textSecondary)
                    }
                }
            }

            Spacer()
        }
    }

    @ViewBuilder
    private func row(label: String, value: String) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(label)
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textSecondary)
            Text(value)
                .font(HygurTypography.cardMeta)
                .foregroundStyle(HygurColors.textPrimary)
                .textSelection(.enabled)
        }
    }

    private func formatDate(_ raw: String) -> String {
        let iso = ISO8601DateFormatter()
        iso.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let d = iso.date(from: raw) { return Self.short.string(from: d) }
        iso.formatOptions = [.withInternetDateTime]
        if let d = iso.date(from: raw) { return Self.short.string(from: d) }
        return raw
    }

    private static let short: DateFormatter = {
        let f = DateFormatter()
        f.dateStyle = .medium
        f.timeStyle = .short
        return f
    }()
}
