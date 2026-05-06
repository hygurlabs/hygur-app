import SwiftUI

/// First-run guided setup. Five steps, the welcome and final ones are
/// non-skippable; the three middle ones (model / mail / folder) all offer
/// "Skip for now" so users who want to defer setup still land in a usable
/// state. The view tracks its own current step via @State; persistence of
/// the "completed" flag lives in HygurApp via @AppStorage.
struct OnboardingView: View {
    /// Called when the user finishes the flow (either via Start chatting on
    /// the final step, or when they skip the last optional step). The host is
    /// responsible for flipping the `onboarding.completed` flag and dismissing
    /// the sheet.
    let onComplete: () -> Void

    @State private var step: OnboardingStep = .welcome

    var body: some View {
        VStack(spacing: 0) {
            stepContent
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .transition(.opacity.combined(with: .move(edge: .trailing)))

            footer
        }
        .frame(width: 760, height: 580)
        .background(HygurColors.background)
        .animation(.easeInOut(duration: 0.18), value: step)
    }

    // MARK: - Step routing

    @ViewBuilder
    private var stepContent: some View {
        switch step {
        case .welcome:
            StepWelcome(onGetStarted: { advance() })
        case .connectModel:
            StepConnectModel(onAdvance: { advance() })
        case .connectMail:
            StepConnectMail()
        case .importFolder:
            StepImportFolder()
        case .ready:
            StepReady()
        }
    }

    // MARK: - Footer

    private var footer: some View {
        HStack(spacing: HygurSpacing.lg) {
            // Back button — visible from step 2 onwards. Welcome has no
            // history, the final step uses Finish instead of Next so back
            // remains useful in case the user wants to revise an answer.
            if step != .welcome {
                Button("Back") { regress() }
                    .buttonStyle(.plain)
                    .foregroundStyle(HygurColors.textSecondary)
                    .keyboardShortcut(.cancelAction)
            }

            Spacer()

            OnboardingProgressDots(
                total: OnboardingStep.allCases.count,
                currentIndex: step.rawValue
            )

            Spacer()

            // Skip button — only on the optional middle steps.
            if step.isSkippable {
                Button("Skip for now") { advance() }
                    .buttonStyle(.plain)
                    .foregroundStyle(HygurColors.textSecondary)
            }

            // Primary action — "Get started" appears inline in the welcome
            // step itself; the connect-model step owns its own "Test &
            // continue" so it can validate before advancing; the final step
            // surfaces "Start chatting" here; everything else falls through
            // to a generic "Continue".
            if step == .ready {
                Button(action: onComplete) {
                    Text("Start chatting")
                        .frame(minWidth: 140)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .keyboardShortcut(.defaultAction)
                .tint(HygurColors.accent)
            } else if step != .welcome && !step.ownsPrimaryAction {
                Button(action: { advance() }) {
                    Text("Continue")
                        .frame(minWidth: 120)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.regular)
                .keyboardShortcut(.defaultAction)
                .tint(HygurColors.accent)
            }
        }
        .padding(.horizontal, HygurSpacing.xxl)
        .padding(.vertical, HygurSpacing.lg)
        .background(
            HygurColors.surface
                .overlay(alignment: .top) {
                    Rectangle()
                        .fill(HygurColors.border)
                        .frame(height: 1)
                }
        )
    }

    // MARK: - Navigation

    private func advance() {
        if let next = step.next {
            step = next
        } else {
            onComplete()
        }
    }

    private func regress() {
        if let previous = step.previous {
            step = previous
        }
    }
}
