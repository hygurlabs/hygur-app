import SwiftUI

/// Informational step. Lists every macOS permission Hygur may request, why
/// it's needed, and when. The page does NOT actually trigger any system
/// prompts — those still happen lazily the first time the matching feature
/// is used (mic on first push-to-talk, calendar on opening Agenda, etc.).
/// Showing this up-front keeps users who haven't audited the source code in
/// the loop without surprising them with a stack of permission dialogs.
struct StepPermissions: View {
    var body: some View {
        VStack(spacing: HygurSpacing.xxl) {
            VStack(spacing: HygurSpacing.sm) {
                Image(systemName: "lock.shield")
                    .font(.system(size: 40, weight: .light))
                    .foregroundStyle(HygurColors.accent)

                Text("What Hygur may ask for")
                    .font(HygurTypography.title2)
                    .foregroundStyle(HygurColors.textPrimary)

                Text("Hygur asks for these only when you use the matching feature — never up-front. You can review or revoke any of them at any time in System Settings.")
                    .font(HygurTypography.body)
                    .foregroundStyle(HygurColors.textSecondary)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 540)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .padding(.top, HygurSpacing.xl)

            VStack(alignment: .leading, spacing: HygurSpacing.lg) {
                permissionRow(
                    icon: "mic.fill",
                    title: "Microphone",
                    detail: "For push-to-talk voice input in the chat. Asked the first time you hold the mic button."
                )
                permissionRow(
                    icon: "waveform",
                    title: "Speech Recognition",
                    detail: "Transcribes your voice on-device using Apple Speech. Same first-use trigger as the microphone."
                )
                permissionRow(
                    icon: "calendar",
                    title: "Calendar",
                    detail: "Reads upcoming events to surface them in your agenda and creates new events on your behalf — every create asks you to confirm first."
                )
                permissionRow(
                    icon: "bell.badge",
                    title: "Notifications",
                    detail: "Daily briefs and priority alerts. Opt-in: nothing fires until you turn them on in Settings → Notifications."
                )
            }
            .frame(maxWidth: 560, alignment: .leading)

            Spacer()
        }
        .padding(.horizontal, HygurSpacing.xxxxl)
        .padding(.vertical, HygurSpacing.xl)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
    }

    @ViewBuilder
    private func permissionRow(icon: String, title: String, detail: String) -> some View {
        HStack(alignment: .top, spacing: HygurSpacing.md) {
            Image(systemName: icon)
                .font(.system(size: 18, weight: .regular))
                .foregroundStyle(HygurColors.accent)
                .frame(width: 32, height: 32, alignment: .center)

            VStack(alignment: .leading, spacing: HygurSpacing.xxs) {
                Text(title)
                    .font(HygurTypography.subheadline.weight(.semibold))
                    .foregroundStyle(HygurColors.textPrimary)
                Text(detail)
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textSecondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }
}
