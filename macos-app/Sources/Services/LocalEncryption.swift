import Foundation
import Security

/// Local database encryption (SQLCipher) opt-in.
///
/// The key lives in the macOS Keychain; `SidecarSupervisor` passes it to the
/// sidecar as `HYGUR_DB_KEY` on launch, and the sidecar auto-migrates the
/// plaintext database to encrypted on the first keyed run (see `store.Open`).
/// "Enabled" is simply "a key exists in the Keychain" — there's no separate
/// flag that could drift out of sync with the actual key.
enum LocalEncryption {
    private static let account = "db-key"

    /// Whether local encryption is on (a key is stored in the Keychain).
    static var isEnabled: Bool { KeychainService.loadSystemSecret(account) != nil }

    /// The key to hand the sidecar, or nil when encryption is off.
    static func keyIfEnabled() -> String? { KeychainService.loadSystemSecret(account) }

    /// Turn on local encryption: generate a 256-bit key and store it in the
    /// Keychain. Idempotent. The DB migration itself happens in the sidecar on
    /// its next (keyed) launch — so callers should restart the sidecar after.
    static func enable() throws {
        if isEnabled { return }
        try KeychainService.saveSystemSecret(account, value: randomKeyHex())
    }

    /// 256-bit cryptographically-random key, hex-encoded — used as the SQLCipher
    /// passphrase (SQLCipher applies its own KDF).
    private static func randomKeyHex() -> String {
        var bytes = [UInt8](repeating: 0, count: 32)
        _ = SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes)
        return bytes.map { String(format: "%02x", $0) }.joined()
    }
}
