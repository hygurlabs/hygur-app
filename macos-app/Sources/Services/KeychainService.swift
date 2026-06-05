import Foundation
import Security

enum KeychainService {
    private static func serviceIdentifier(connectorId: String, key: String) -> String {
        "com.hygur.connector.\(connectorId).\(key)"
    }

    static func save(connectorId: String, key: String, value: String) throws {
        guard let data = value.data(using: .utf8) else { return }

        let service = serviceIdentifier(connectorId: connectorId, key: key)

        let deleteQuery: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: key
        ]
        SecItemDelete(deleteQuery as CFDictionary)

        let addQuery: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: key,
            kSecValueData as String: data,
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        ]

        let status = SecItemAdd(addQuery as CFDictionary, nil)
        guard status == errSecSuccess else {
            throw SidecarError.keychainError(status: status)
        }
    }

    static func load(connectorId: String, key: String) -> String? {
        let service = serviceIdentifier(connectorId: connectorId, key: key)

        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: key,
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

    static func delete(connectorId: String, key: String) {
        let service = serviceIdentifier(connectorId: connectorId, key: key)

        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: key
        ]
        SecItemDelete(query as CFDictionary)
    }

    static func loadSecrets(connectorId: String, schema: ConnectorConfigSchema) -> [String: String] {
        var secrets: [String: String] = [:]
        for group in schema.groups {
            for field in group.fields where field.fieldType == "secret" || field.fieldType == "oauth" {
                if let value = load(connectorId: connectorId, key: field.key) {
                    secrets[field.key] = value
                }
            }
        }
        return secrets
    }

    // MARK: - System secrets (not tied to a connector) — e.g. the local DB key.

    static func saveSystemSecret(_ account: String, value: String) throws {
        try save(connectorId: "_system", key: account, value: value)
    }

    static func loadSystemSecret(_ account: String) -> String? {
        load(connectorId: "_system", key: account)
    }

    static func deleteSystemSecret(_ account: String) {
        delete(connectorId: "_system", key: account)
    }
}
