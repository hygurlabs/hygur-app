import SwiftUI
import AppKit

/// Banner surfaced at the top of the main window when the configured AI
/// runtime URL is reachable but the runtime itself isn't responding (or
/// hasn't been configured at all). The point is to make the failure mode
/// loud — the chat-side error message ("model offline") is too easy to miss
/// for users who just installed Hygur and haven't started LM Studio / vLLM
/// yet, so they wonder why nothing answers.
///
/// Hidden by default. The owning view (ContentView) controls visibility
/// via `eventStream.lmStudioStatus == .down` plus a per-session dismissal
/// flag — we don't auto-redisplay on every redraw if the user already
/// chose to ignore it.
struct RuntimeUnreachableBanner: View {
    let onConfigure: () -> Void
    let onDismiss: () -> Void

    var body: some View {
        HStack(spacing: HygurSpacing.md) {
            Image(systemName: "cpu")
                .font(.system(size: 16, weight: .semibold))
                .foregroundStyle(HygurColors.warning)
                .accessibilityHidden(true)

            VStack(alignment: .leading, spacing: 2) {
                Text("AI runtime is offline")
                    .font(HygurTypography.subheadline.weight(.semibold))
                    .foregroundStyle(HygurColors.textPrimary)
                Text("Chat replies will fail until your local model is reachable. Configure or start it to continue.")
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textSecondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            Spacer()

            Button("Configure now", action: onConfigure)
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
                .tint(HygurColors.accent)

            Button {
                onDismiss()
            } label: {
                Image(systemName: "xmark")
                    .foregroundStyle(HygurColors.textSecondary)
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Dismiss")
        }
        .padding(.horizontal, HygurSpacing.lg)
        .padding(.vertical, HygurSpacing.md)
        .background(HygurColors.warning.opacity(0.08))
        .overlay(alignment: .bottom) {
            Rectangle()
                .fill(HygurColors.warning.opacity(0.3))
                .frame(height: 1)
        }
    }
}
