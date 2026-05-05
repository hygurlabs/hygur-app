import Foundation
import Security

/// Service for securely storing mail credentials in macOS Keychain
enum MailKeychainService {
    private static let service = "com.hygur.mail"

    // MARK: - Proton Bridge Credentials

    /// Save Proton Bridge credentials to Keychain
    static func saveProtonCredentials(username: String, password: String) throws {
        try saveCredential(account: "proton.username", value: username)
        try saveCredential(account: "proton.password", value: password)
    }

    /// Load Proton Bridge credentials from Keychain
    static func loadProtonCredentials() -> (username: String, password: String)? {
        guard let username = loadCredential(account: "proton.username"),
              let password = loadCredential(account: "proton.password") else {
            return nil
        }
        return (username, password)
    }

    /// Delete Proton Bridge credentials from Keychain
    static func deleteProtonCredentials() {
        deleteCredential(account: "proton.username")
        deleteCredential(account: "proton.password")
    }

    // MARK: - Gmail OAuth Credentials

    /// Save Gmail OAuth credentials to Keychain
    static func saveGmailCredentials(clientId: String, clientSecret: String) throws {
        try saveCredential(account: "gmail.client_id", value: clientId)
        try saveCredential(account: "gmail.client_secret", value: clientSecret)
    }

    /// Load Gmail OAuth credentials from Keychain
    static func loadGmailCredentials() -> (clientId: String, clientSecret: String)? {
        guard let clientId = loadCredential(account: "gmail.client_id"),
              let clientSecret = loadCredential(account: "gmail.client_secret") else {
            return nil
        }
        return (clientId, clientSecret)
    }

    /// Delete Gmail OAuth credentials from Keychain
    static func deleteGmailCredentials() {
        deleteCredential(account: "gmail.client_id")
        deleteCredential(account: "gmail.client_secret")
    }

    /// Save Gmail access token to Keychain
    static func saveGmailToken(_ token: String) throws {
        try saveCredential(account: "gmail.token", value: token)
    }

    /// Load Gmail access token from Keychain
    static func loadGmailToken() -> String? {
        return loadCredential(account: "gmail.token")
    }

    /// Delete Gmail access token from Keychain
    static func deleteGmailToken() {
        deleteCredential(account: "gmail.token")
    }

    // MARK: - Private Helpers

    private static func saveCredential(account: String, value: String) throws {
        guard let data = value.data(using: .utf8) else {
            throw MailKeychainError.encodingFailed
        }

        // Delete existing item first
        let deleteQuery: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account
        ]
        SecItemDelete(deleteQuery as CFDictionary)

        // Add new item
        let addQuery: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecValueData as String: data,
            kSecAttrAccessible as String: kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        ]

        let status = SecItemAdd(addQuery as CFDictionary, nil)
        guard status == errSecSuccess else {
            throw MailKeychainError.saveFailed(status: status)
        }
    }

    private static func loadCredential(account: String) -> String? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne
        ]

        var result: AnyObject?
        let status = SecItemCopyMatching(query as CFDictionary, &result)

        guard status == errSecSuccess,
              let data = result as? Data,
              let value = String(data: data, encoding: .utf8) else {
            return nil
        }

        return value
    }

    private static func deleteCredential(account: String) {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account
        ]
        SecItemDelete(query as CFDictionary)
    }
}

enum MailKeychainError: LocalizedError {
    case encodingFailed
    case saveFailed(status: OSStatus)

    var errorDescription: String? {
        switch self {
        case .encodingFailed:
            return "Failed to encode credential"
        case .saveFailed(let status):
            return "Failed to save to Keychain (status: \(status))"
        }
    }
}
