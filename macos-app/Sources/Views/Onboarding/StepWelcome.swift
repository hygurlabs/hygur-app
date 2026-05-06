import SwiftUI

struct StepWelcome: View {
    let onGetStarted: () -> Void

    var body: some View {
        VStack(spacing: HygurSpacing.xxxl) {
            Spacer()

            VStack(spacing: HygurSpacing.lg) {
                ZStack {
                    Circle()
                        .fill(HygurColors.accentGradient)
                        .frame(width: 120, height: 120)
                    Image(systemName: "brain.head.profile")
                        .font(.system(size: 56, weight: .light))
                        .foregroundStyle(HygurColors.accent)
                }

                VStack(spacing: HygurSpacing.sm) {
                    Text("Welcome to Hygur")
                        .font(HygurTypography.largeTitle)
                        .foregroundStyle(HygurColors.textPrimary)

                    Text("Your local-first knowledge base and AI assistant.")
                        .font(HygurTypography.title3)
                        .foregroundStyle(HygurColors.textSecondary)
                        .multilineTextAlignment(.center)
                }
            }

            VStack(alignment: .leading, spacing: HygurSpacing.lg) {
                bullet(
                    icon: "lock.shield",
                    title: "Private by design",
                    body: "Your documents, notes, and conversations stay on this Mac. Hygur talks to a local AI runtime you control."
                )
                bullet(
                    icon: "sparkles",
                    title: "Multimodal & extensible",
                    body: "Bring your own model — chat, search, summarize. Connect mail and folders to ground answers in your own data."
                )
                bullet(
                    icon: "bolt.horizontal.fill",
                    title: "Three quick steps",
                    body: "Connect a model, optionally link a mailbox, optionally import a folder. You can change everything later in Settings."
                )
            }
            .frame(maxWidth: 480, alignment: .leading)

            Spacer()

            Button(action: onGetStarted) {
                Text("Get started")
                    .font(HygurTypography.headline)
                    .frame(maxWidth: 200)
                    .padding(.vertical, HygurSpacing.sm)
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.large)
            .keyboardShortcut(.defaultAction)
            .tint(HygurColors.accent)
        }
        .padding(.horizontal, HygurSpacing.xxxxl)
        .padding(.vertical, HygurSpacing.xxl)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    @ViewBuilder
    private func bullet(icon: String, title: String, body: String) -> some View {
        HStack(alignment: .top, spacing: HygurSpacing.md) {
            Image(systemName: icon)
                .font(.system(size: 18, weight: .regular))
                .foregroundStyle(HygurColors.accent)
                .frame(width: 28, height: 28, alignment: .center)

            VStack(alignment: .leading, spacing: HygurSpacing.xxs) {
                Text(title)
                    .font(HygurTypography.subheadline.weight(.semibold))
                    .foregroundStyle(HygurColors.textPrimary)
                Text(body)
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textSecondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }
}
