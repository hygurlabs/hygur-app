import SwiftUI
import UserNotifications

/// Onboarding step that surfaces the two opt-in notification categories
/// (daily brief, priority email) before the system permission prompt fires.
/// Permission is requested lazily — only when the user flips a toggle on —
/// so users who skip this step never see a UNUserNotificationCenter prompt.
///
/// Skippable. The same toggles live in Settings → Notifications, so coming
/// here later is harmless.
struct StepNotifications: View {
    @State private var dailyBrief: Bool = UserDefaults.standard.bool(forKey: "notify.dailyBrief")
    @State private var priorityMail: Bool = UserDefaults.standard.bool(forKey: "notify.priorityMail")
    @State private var authorizationStatus: UNAuthorizationStatus = .notDetermined

    var body: some View {
        VStack(spacing: HygurSpacing.xxl) {
            VStack(spacing: HygurSpacing.sm) {
                Image(systemName: "bell.badge")
                    .font(.system(size: 40, weight: .light))
                    .foregroundStyle(HygurColors.accent)

                Text("Stay in the loop")
                    .font(HygurTypography.title2)
                    .foregroundStyle(HygurColors.textPrimary)

                Text("Pick what Hygur is allowed to interrupt you for. Everything else stays silent — visible only in the Activity sidebar inside the app.")
                    .font(HygurTypography.body)
                    .foregroundStyle(HygurColors.textSecondary)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 540)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .padding(.top, HygurSpacing.xl)

            VStack(spacing: HygurSpacing.md) {
                togglesCard

                if authorizationStatus == .denied {
                    deniedHint
                }
            }
            .frame(maxWidth: 560)

            Spacer()
        }
        .padding(.horizontal, HygurSpacing.xxxxl)
        .padding(.vertical, HygurSpacing.xl)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
        .task { await refreshAuthorization() }
        .onReceive(
            NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)
        ) { _ in
            Task { await refreshAuthorization() }
        }
    }

    private var togglesCard: some View {
        VStack(spacing: 0) {
            toggleRow(
                icon: "sun.horizon",
                title: "Daily brief",
                description: "A summary of your day each morning.",
                isOn: $dailyBrief,
                key: "notify.dailyBrief"
            )

            Divider().background(HygurColors.border)

            toggleRow(
                icon: "envelope.badge",
                title: "Priority emails",
                description: "Banner when a high-importance message arrives.",
                isOn: $priorityMail,
                key: "notify.priorityMail"
            )
        }
        .background(
            RoundedRectangle(cornerRadius: HygurRadius.lg, style: .continuous)
                .fill(HygurColors.surface)
        )
    }

    private func toggleRow(
        icon: String,
        title: String,
        description: String,
        isOn: Binding<Bool>,
        key: String
    ) -> some View {
        HStack(alignment: .top, spacing: HygurSpacing.md) {
            Image(systemName: icon)
                .font(.system(size: 18, weight: .regular))
                .foregroundStyle(HygurColors.accent)
                .frame(width: 32, height: 32, alignment: .center)

            VStack(alignment: .leading, spacing: HygurSpacing.xxs) {
                Text(title)
                    .font(HygurTypography.subheadline.weight(.semibold))
                    .foregroundStyle(HygurColors.textPrimary)
                Text(description)
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textSecondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            Spacer()

            Toggle("", isOn: isOn)
                .labelsHidden()
                .toggleStyle(.switch)
                .tint(HygurColors.accent)
                .onChange(of: isOn.wrappedValue) { _, newValue in
                    UserDefaults.standard.set(newValue, forKey: key)
                    if newValue {
                        Task {
                            await NotificationsService.shared.ensureAuthorization()
                            await refreshAuthorization()
                        }
                    }
                }
        }
        .padding(HygurSpacing.lg)
    }

    /// Surfaces the case where the user enabled a category but the system
    /// permission has been denied (either earlier in this run, in a previous
    /// install, or via System Settings). Without this hint the toggles look
    /// armed but no banner ever fires.
    private var deniedHint: some View {
        HStack(alignment: .top, spacing: HygurSpacing.sm) {
            Image(systemName: "exclamationmark.triangle.fill")
                .font(.system(size: 14))
                .foregroundStyle(HygurColors.warning)
            VStack(alignment: .leading, spacing: HygurSpacing.xxs) {
                Text("Notifications are blocked at the system level.")
                    .font(HygurTypography.caption.weight(.semibold))
                    .foregroundStyle(HygurColors.textPrimary)
                Text("Open System Settings → Notifications → Hygur to allow them.")
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textSecondary)
            }
            Spacer()
            Button("Open Settings") {
                if let url = URL(string: "x-apple.systempreferences:com.apple.preference.notifications") {
                    NSWorkspace.shared.open(url)
                }
            }
            .buttonStyle(.bordered)
            .controlSize(.small)
        }
        .padding(HygurSpacing.md)
        .background(
            RoundedRectangle(cornerRadius: HygurRadius.md, style: .continuous)
                .fill(HygurColors.warning.opacity(0.1))
        )
    }

    private func refreshAuthorization() async {
        let settings = await UNUserNotificationCenter.current().notificationSettings()
        authorizationStatus = settings.authorizationStatus
    }
}
