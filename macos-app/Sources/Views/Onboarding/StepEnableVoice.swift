import SwiftUI
import AppKit
import Speech

/// Step 5 of the onboarding flow — surfaces whether on-device speech
/// recognition is installed for one of the user's preferred languages.
/// We force on-device for privacy; if the pack isn't installed the mic is
/// disabled in the chat, so we tell the user up-front and offer a
/// deeplink to the right pane in System Settings rather than letting them
/// discover the limitation by clicking a dead button later.
///
/// Skippable — voice is convenience, not a hard requirement.
struct StepEnableVoice: View {
    @Environment(VoiceService.self) private var voiceService
    @State private var didOpenSettings: Bool = false

    var body: some View {
        VStack(spacing: HygurSpacing.lg) {
            header

            statusCard
                .padding(.horizontal, HygurSpacing.xxxl)
                .frame(maxWidth: 580)

            Spacer()
        }
        .padding(.top, HygurSpacing.xl)
        .padding(.bottom, HygurSpacing.lg)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .task {
            // The app already pre-warms VoiceService at launch, but redo it
            // here in case the user just changed their language preferences
            // and came back to this step — prepare() is idempotent and the
            // re-run picks up the freshly installed pack.
            if didOpenSettings {
                await voiceService.prepare()
            }
        }
    }

    private var header: some View {
        VStack(spacing: HygurSpacing.sm) {
            Image(systemName: "mic.fill")
                .font(.system(size: 36, weight: .light))
                .foregroundStyle(HygurColors.accent)
            Text("Enable voice input")
                .font(HygurTypography.title)
                .foregroundStyle(HygurColors.textPrimary)
            Text("Hygur uses Apple's on-device speech recognition so audio never leaves this Mac. You'll need a language pack installed for one of your preferred languages.")
                .font(HygurTypography.body)
                .foregroundStyle(HygurColors.textSecondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 480)
        }
    }

    @ViewBuilder
    private var statusCard: some View {
        switch authorizationState {
        case .denied, .restricted:
            permissionDeniedCard
        case .notDetermined:
            preparingCard
        case .authorized where voiceService.isOnDeviceAvailable:
            availableCard
        case .authorized:
            packMissingCard
        @unknown default:
            preparingCard
        }
    }

    /// "Authorized + pack installed" — green checkmark, locale name, done.
    private var availableCard: some View {
        HStack(spacing: HygurSpacing.md) {
            Image(systemName: "checkmark.seal.fill")
                .font(.system(size: 22))
                .foregroundStyle(HygurColors.success)
            VStack(alignment: .leading, spacing: 2) {
                Text("Voice is ready")
                    .font(HygurTypography.subheadline.weight(.medium))
                    .foregroundStyle(HygurColors.textPrimary)
                Text(localeDescription)
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textSecondary)
            }
            Spacer()
        }
        .padding(HygurSpacing.lg)
        .background(
            RoundedRectangle(cornerRadius: HygurRadius.lg, style: .continuous)
                .fill(HygurColors.surface)
        )
    }

    /// "Authorized but no on-device pack for any preferred language" — the
    /// most common case for users on macOS 26 with a non-English locale who
    /// haven't installed Dictation. Deeplink straight to the right pane;
    /// after they return we re-run prepare().
    private var packMissingCard: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.md) {
            HStack(spacing: HygurSpacing.md) {
                Image(systemName: "exclamationmark.triangle.fill")
                    .font(.system(size: 22))
                    .foregroundStyle(HygurColors.warning)
                VStack(alignment: .leading, spacing: 2) {
                    Text("No on-device language pack installed")
                        .font(HygurTypography.subheadline.weight(.medium))
                        .foregroundStyle(HygurColors.textPrimary)
                    Text("Open System Settings → Keyboard → Dictation and enable a language. Hygur will detect it next time you record.")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                Spacer()
            }
            HStack {
                Spacer()
                Button("Open Dictation Settings", action: openDictationSettings)
                    .buttonStyle(.borderedProminent)
                    .controlSize(.regular)
                    .tint(HygurColors.accent)
            }
        }
        .padding(HygurSpacing.lg)
        .background(
            RoundedRectangle(cornerRadius: HygurRadius.lg, style: .continuous)
                .fill(HygurColors.surface)
        )
    }

    /// "User denied permission" — we can't recover with a deeplink to
    /// Dictation, they have to flip the toggle in Privacy & Security.
    private var permissionDeniedCard: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.md) {
            HStack(spacing: HygurSpacing.md) {
                Image(systemName: "xmark.shield.fill")
                    .font(.system(size: 22))
                    .foregroundStyle(HygurColors.danger)
                VStack(alignment: .leading, spacing: 2) {
                    Text("Permission denied")
                        .font(HygurTypography.subheadline.weight(.medium))
                        .foregroundStyle(HygurColors.textPrimary)
                    Text("Hygur needs permission to use speech recognition and the microphone. Grant access in System Settings → Privacy & Security.")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                Spacer()
            }
            HStack {
                Spacer()
                Button("Open Privacy Settings", action: openPrivacySettings)
                    .buttonStyle(.borderedProminent)
                    .controlSize(.regular)
                    .tint(HygurColors.accent)
            }
        }
        .padding(HygurSpacing.lg)
        .background(
            RoundedRectangle(cornerRadius: HygurRadius.lg, style: .continuous)
                .fill(HygurColors.surface)
        )
    }

    /// "Still resolving" — covers the brief window between view appearance
    /// and prepare() finishing, plus the case where the user hasn't been
    /// prompted for permission yet.
    private var preparingCard: some View {
        HStack(spacing: HygurSpacing.md) {
            ProgressView().controlSize(.small)
            Text("Checking on-device speech availability…")
                .font(HygurTypography.subheadline)
                .foregroundStyle(HygurColors.textSecondary)
            Spacer()
        }
        .padding(HygurSpacing.lg)
        .background(
            RoundedRectangle(cornerRadius: HygurRadius.lg, style: .continuous)
                .fill(HygurColors.surface)
        )
    }

    // MARK: - State helpers

    private var authorizationState: SFSpeechRecognizerAuthorizationStatus {
        SFSpeechRecognizer.authorizationStatus()
    }

    private var localeDescription: String {
        guard let locale = voiceService.resolvedLocale else { return "On-device pack installed." }
        let displayName = locale.localizedString(forIdentifier: locale.identifier) ?? locale.identifier
        return "Recognizing \(displayName) on-device."
    }

    // MARK: - Actions

    /// macOS 26 dropped the legacy `com.apple.preference.keyboard` pane in
    /// favor of the unified Settings app — but the URL scheme below still
    /// resolves and lands on Keyboard › Dictation. If Apple breaks it again
    /// the user just sees the top of Settings and the explanatory text in
    /// the card guides them.
    private func openDictationSettings() {
        if let url = URL(string: "x-apple.systempreferences:com.apple.preference.keyboard?Dictation") {
            NSWorkspace.shared.open(url)
            didOpenSettings = true
        }
    }

    private func openPrivacySettings() {
        if let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_SpeechRecognition") {
            NSWorkspace.shared.open(url)
            didOpenSettings = true
        }
    }
}
