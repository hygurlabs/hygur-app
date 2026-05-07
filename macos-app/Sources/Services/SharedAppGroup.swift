import Foundation

/// Bridge between the main Hygur app and its companions (Share Extension,
/// Services menu provider) for the small amount of state both processes need
/// in order to call the sidecar.
///
/// macOS share extensions are forced into a sandbox by the OS regardless of
/// the host's sandbox setting, which means they cannot read the sidecar
/// token from `~/Library/Application Support/Hygur/token` directly. We mirror
/// the URL + token into an App Group `UserDefaults` suite at app launch and
/// every time the user changes the URL in Settings — the extension reads from
/// the same suite and never has to touch the file system.
///
/// The companion declared the same suite in its entitlements; on dev builds
/// without a Team ID the OS still resolves the suite to
/// `~/Library/Preferences/group.com.hygur.shared.plist`, which is readable
/// from both processes since they run under the same user account.
enum SharedAppGroup {
    /// App Group identifier shared by `Hygur.app` and `HygurShare.appex`.
    static let suiteName = "group.com.hygur.shared"

    /// UserDefaults keys inside the shared suite. Kept under the
    /// `hygur.shared.` namespace so we can later add unrelated values without
    /// risking collisions with `@AppStorage` keys mirrored from `standard`.
    enum Keys {
        static let sidecarURL = "hygur.shared.sidecarURL"
        static let sidecarToken = "hygur.shared.sidecarToken"
    }

    /// The shared `UserDefaults` instance. Returns `nil` only when the suite
    /// cannot be opened — should never happen in practice, but the call
    /// sites all degrade gracefully.
    static var defaults: UserDefaults? {
        UserDefaults(suiteName: suiteName)
    }

    /// Pushes the current sidecar URL + token into the shared suite. Safe to
    /// call repeatedly — overwrites any previous value. The token may be
    /// `nil` on a brand-new install before the sidecar has booted; in that
    /// case we clear the key so the extension reports the dedicated
    /// "sidecar not reachable" error rather than sending an unauthenticated
    /// request that gets rejected with a confusing 401.
    static func writeSidecarConfig(url: String, token: String?) {
        guard let defaults else { return }
        defaults.set(url, forKey: Keys.sidecarURL)
        if let token, !token.isEmpty {
            defaults.set(token, forKey: Keys.sidecarToken)
        } else {
            defaults.removeObject(forKey: Keys.sidecarToken)
        }
    }

    /// Convenience read accessors used by the extension and the Services
    /// menu provider — both want a `(URL, token)` tuple with sensible
    /// defaults baked in.
    static func readSidecarConfig() -> (url: URL, token: String?) {
        let urlString = defaults?.string(forKey: Keys.sidecarURL) ?? "http://localhost:8420"
        let url = URL(string: urlString) ?? URL(string: "http://localhost:8420")!
        let token = defaults?.string(forKey: Keys.sidecarToken)
        return (url, token)
    }
}
