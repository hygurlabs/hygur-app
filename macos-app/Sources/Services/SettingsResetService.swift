import Foundation
import Security

/// Wipes the user's preferences (and optionally cached credentials) without
/// touching their data directory. Surfaced as the "Reset all settings"
/// escape hatch in Settings → System.
///
/// Notes / knowledge base / conversations / backups are stored on disk under
/// `~/Library/Application Support/Hygur/` and are *deliberately untouched*.
/// Users who want a full wipe should reinstall.
enum SettingsResetService {
    /// Keys we keep across a reset. `onboarding.completed` stays so the
    /// user doesn't get the first-run flow shoved at them again every time
    /// they hit reset — there is a separate debug-only "Reset Onboarding"
    /// menu item for that case.
    private static let preservedKeys: Set<String> = [
        "onboarding.completed"
    ]

    struct Outcome {
        let preferenceKeysCleared: Int
        let credentialsCleared: Bool
    }

    /// Clears `UserDefaults` for the app's bundle ID. When `forgetCredentials`
    /// is true, also nukes the sidecar API token and every connector secret
    /// stored under the `com.hygur.connector.*` keychain service prefix.
    @MainActor
    static func reset(forgetCredentials: Bool) -> Outcome {
        let cleared = clearUserDefaults()
        var credentialsCleared = false
        if forgetCredentials {
            clearKeychain()
            credentialsCleared = true
        }
        return Outcome(
            preferenceKeysCleared: cleared,
            credentialsCleared: credentialsCleared
        )
    }

    @discardableResult
    private static func clearUserDefaults() -> Int {
        let defaults = UserDefaults.standard
        let snapshot = defaults.dictionaryRepresentation()
        var removed = 0
        for key in snapshot.keys where !preservedKeys.contains(key) {
            // Skip Apple-managed system keys (NSGlobalDomain leakage). They
            // start with "Apple" / "NS" and removing them does nothing useful
            // — UserDefaults silently ignores them, but counting them would
            // mislead the user into thinking we wiped more than we did.
            if key.hasPrefix("Apple") || key.hasPrefix("NS") || key.hasPrefix("com.apple.") {
                continue
            }
            defaults.removeObject(forKey: key)
            removed += 1
        }
        defaults.synchronize()
        return removed
    }

    /// Removes Hygur-owned keychain entries:
    /// - the sidecar API token (`com.hygur.sidecar` / `api-token`)
    /// - every connector secret stored under a `com.hygur.connector.*` service
    ///
    /// We can't enumerate by service prefix directly, so we ask the keychain
    /// for every generic password whose service starts with `com.hygur.` and
    /// delete each one individually.
    private static func clearKeychain() {
        // Sidecar token — known service+account.
        let tokenQuery: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: "com.hygur.sidecar",
            kSecAttrAccount as String: "api-token",
        ]
        SecItemDelete(tokenQuery as CFDictionary)

        // Connector secrets — enumerate all matching items, then delete each.
        let listQuery: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecMatchLimit as String: kSecMatchLimitAll,
            kSecReturnAttributes as String: true,
        ]
        var result: AnyObject?
        let status = SecItemCopyMatching(listQuery as CFDictionary, &result)
        guard status == errSecSuccess,
              let items = result as? [[String: Any]] else { return }
        for item in items {
            guard let service = item[kSecAttrService as String] as? String,
                  service.hasPrefix("com.hygur.connector.") else { continue }
            let account = item[kSecAttrAccount as String] as? String ?? ""
            let deleteQuery: [String: Any] = [
                kSecClass as String: kSecClassGenericPassword,
                kSecAttrService as String: service,
                kSecAttrAccount as String: account,
            ]
            SecItemDelete(deleteQuery as CFDictionary)
        }
    }
}
