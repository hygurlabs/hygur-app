import Foundation
import ServiceManagement

/// `LaunchAgentService` thinly wraps `SMAppService.mainApp` so the user can
/// toggle "Run Hygur at login" from Settings. macOS 13+. The user can
/// always override via System Settings → Login Items.
@MainActor
final class LaunchAgentService {
    static let shared = LaunchAgentService()
    private init() {}

    /// True if the macOS app is currently registered as a login item.
    var isRegistered: Bool {
        SMAppService.mainApp.status == .enabled
    }

    /// Register the macOS app as a login item.
    func register() throws {
        try SMAppService.mainApp.register()
    }

    /// Unregister the macOS app as a login item.
    func unregister() throws {
        try SMAppService.mainApp.unregister()
    }

    /// User-visible status string for the Settings row.
    var statusDescription: String {
        switch SMAppService.mainApp.status {
        case .notRegistered: return "Not registered"
        case .enabled: return "Enabled"
        case .requiresApproval: return "Requires approval in System Settings"
        case .notFound: return "Not found"
        @unknown default: return "Unknown"
        }
    }
}
