import Foundation
import CommonCrypto
import CryptoKit

/// Encrypted backup/restore of the Hygur data directory.
///
/// File format (`.hygurbackup`):
///
///     magic[8]    "HYGRBKP1"
///     version[1]  0x01
///     salt[16]    PBKDF2-HMAC-SHA256 salt
///     iters[4]    PBKDF2 iteration count, big-endian uint32
///     sealed[]    AES-GCM combined box (nonce 12 || ct || tag 16)
///
/// The plaintext payload is a `.tar.gz` of the Hygur application support
/// directory. The GCM tag doubles as integrity check — wrong passphrase or
/// tampered bytes cause `seal(open:)` to throw before we touch the disk.
///
/// **Not in the backup**: macOS Keychain entries (LM Studio token, mail
/// OAuth tokens). Those are stored system-wide, survive a reset, and are
/// re-bound to the user/device. The user is warned in the UI.
@MainActor
enum BackupService {
    enum BackupError: LocalizedError {
        case dataDirMissing
        case archiveFailed(String)
        case extractionFailed(String)
        case decryptionFailed
        case malformedBackup
        case unsupportedVersion(UInt8)
        case write(Error)

        var errorDescription: String? {
            switch self {
            case .dataDirMissing: return "Hygur data directory not found."
            case .archiveFailed(let s): return "Could not archive data: \(s)"
            case .extractionFailed(let s): return "Could not extract backup: \(s)"
            case .decryptionFailed: return "Wrong passphrase or corrupted backup."
            case .malformedBackup: return "This file is not a valid Hygur backup."
            case .unsupportedVersion(let v): return "Unsupported backup format (version \(v))."
            case .write(let e): return "Could not write file: \(e.localizedDescription)"
            }
        }
    }

    // MARK: - Constants

    private static let magic: [UInt8] = Array("HYGRBKP1".utf8) // 8 bytes
    private static let version: UInt8 = 0x01
    private static let saltSize = 16
    private static let pbkdf2Iterations: UInt32 = 600_000
    private static let keySize = 32 // AES-256

    // MARK: - Paths

    /// `~/Library/Application Support/Hygur` — the canonical data dir
    /// shared with the sidecar.
    static var dataDirectoryURL: URL {
        let appSupport = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
        return appSupport.appendingPathComponent("Hygur", isDirectory: true)
    }

    // MARK: - Export

    /// Creates an encrypted `.hygurbackup` at `destination`. Caller is
    /// responsible for prompting and validating the passphrase.
    static func exportBackup(to destination: URL, passphrase: String) async throws {
        let dataDir = dataDirectoryURL
        guard FileManager.default.fileExists(atPath: dataDir.path) else {
            throw BackupError.dataDirMissing
        }

        // Tar+gzip into a temp file so we don't materialize the whole thing
        // in memory before encryption.
        let tempArchive = try await tarGzip(directory: dataDir)
        defer { try? FileManager.default.removeItem(at: tempArchive) }

        let plaintext = try Data(contentsOf: tempArchive, options: .mappedIfSafe)

        var salt = [UInt8](repeating: 0, count: saltSize)
        let saltStatus = SecRandomCopyBytes(kSecRandomDefault, saltSize, &salt)
        guard saltStatus == errSecSuccess else {
            throw BackupError.write(NSError(domain: "BackupService", code: Int(saltStatus)))
        }

        let key = try deriveKey(passphrase: passphrase, salt: Data(salt), iterations: pbkdf2Iterations)
        let sealed = try AES.GCM.seal(plaintext, using: key)
        guard let combined = sealed.combined else {
            throw BackupError.write(NSError(domain: "BackupService", code: -1, userInfo: [NSLocalizedDescriptionKey: "GCM seal returned no combined box"]))
        }

        var output = Data()
        output.append(contentsOf: magic)
        output.append(version)
        output.append(contentsOf: salt)
        var itersBE = pbkdf2Iterations.bigEndian
        withUnsafeBytes(of: &itersBE) { output.append(contentsOf: $0) }
        output.append(combined)

        do {
            try output.write(to: destination, options: .atomic)
        } catch {
            throw BackupError.write(error)
        }
    }

    // MARK: - Restore

    /// Decrypts the backup at `source` and replaces the data dir contents.
    /// The previous data dir is renamed to `Hygur.backup-<timestamp>` as a
    /// safety net; nothing is destroyed in this call. Caller is
    /// responsible for stopping the sidecar before invoking this.
    @discardableResult
    static func restoreBackup(from source: URL, passphrase: String) async throws -> URL? {
        let raw: Data
        do {
            raw = try Data(contentsOf: source, options: .mappedIfSafe)
        } catch {
            throw BackupError.write(error)
        }

        let header = try parseHeader(raw)
        let key = try deriveKey(passphrase: passphrase, salt: header.salt, iterations: header.iterations)

        let plaintext: Data
        do {
            let box = try AES.GCM.SealedBox(combined: header.combined)
            plaintext = try AES.GCM.open(box, using: key)
        } catch {
            // Wrong passphrase, tampered file, or truncation — all surface
            // here. Don't leak which one.
            throw BackupError.decryptionFailed
        }

        // Stage decrypted archive on disk so we can hand it to /usr/bin/tar.
        let tempDir = FileManager.default.temporaryDirectory
            .appendingPathComponent("hygur-restore-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: tempDir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: tempDir) }

        let archiveFile = tempDir.appendingPathComponent("backup.tar.gz")
        try plaintext.write(to: archiveFile, options: .atomic)

        let extractDir = tempDir.appendingPathComponent("extracted", isDirectory: true)
        try FileManager.default.createDirectory(at: extractDir, withIntermediateDirectories: true)
        try await tarExtract(archive: archiveFile, into: extractDir)

        // The archive root is the "Hygur" directory itself; locate it.
        let extractedRoot = extractDir.appendingPathComponent("Hygur", isDirectory: true)
        guard FileManager.default.fileExists(atPath: extractedRoot.path) else {
            throw BackupError.extractionFailed("Backup does not contain a Hygur directory.")
        }

        // Rename the existing data dir aside (don't delete) before swapping.
        let appSupport = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
        let target = appSupport.appendingPathComponent("Hygur", isDirectory: true)

        var rescuedURL: URL?
        if FileManager.default.fileExists(atPath: target.path) {
            let stamp = filenameTimestamp(Date())
            let rescue = appSupport.appendingPathComponent("Hygur.backup-\(stamp)", isDirectory: true)
            try FileManager.default.moveItem(at: target, to: rescue)
            rescuedURL = rescue
        }

        do {
            try FileManager.default.moveItem(at: extractedRoot, to: target)
        } catch {
            // Rollback: put the rescued dir back if the swap failed.
            if let rescued = rescuedURL {
                try? FileManager.default.moveItem(at: rescued, to: target)
            }
            throw BackupError.write(error)
        }

        return rescuedURL
    }

    // MARK: - Header parsing

    private struct Header {
        let salt: Data
        let iterations: UInt32
        let combined: Data
    }

    private static func parseHeader(_ raw: Data) throws -> Header {
        let headerLen = magic.count + 1 + saltSize + 4
        guard raw.count > headerLen else { throw BackupError.malformedBackup }

        let magicBytes = Array(raw.prefix(magic.count))
        guard magicBytes == magic else { throw BackupError.malformedBackup }

        let v = raw[raw.startIndex + magic.count]
        guard v == version else { throw BackupError.unsupportedVersion(v) }

        let saltStart = raw.startIndex + magic.count + 1
        let salt = raw.subdata(in: saltStart..<(saltStart + saltSize))

        let itersStart = saltStart + saltSize
        let itersBytes = raw.subdata(in: itersStart..<(itersStart + 4))
        let iterations = itersBytes.withUnsafeBytes { ptr -> UInt32 in
            UInt32(bigEndian: ptr.load(as: UInt32.self))
        }

        let combinedStart = itersStart + 4
        let combined = raw.subdata(in: combinedStart..<raw.endIndex)
        return Header(salt: salt, iterations: iterations, combined: combined)
    }

    // MARK: - Crypto

    private static func deriveKey(passphrase: String, salt: Data, iterations: UInt32) throws -> SymmetricKey {
        let passphraseData = Data(passphrase.utf8)
        var derived = [UInt8](repeating: 0, count: keySize)

        let status = passphraseData.withUnsafeBytes { passBytes -> Int32 in
            salt.withUnsafeBytes { saltBytes -> Int32 in
                CCKeyDerivationPBKDF(
                    CCPBKDFAlgorithm(kCCPBKDF2),
                    passBytes.baseAddress?.assumingMemoryBound(to: Int8.self), passphraseData.count,
                    saltBytes.baseAddress?.assumingMemoryBound(to: UInt8.self), salt.count,
                    CCPseudoRandomAlgorithm(kCCPRFHmacAlgSHA256),
                    iterations,
                    &derived, derived.count
                )
            }
        }

        guard status == kCCSuccess else {
            throw BackupError.write(NSError(domain: "BackupService.PBKDF2", code: Int(status)))
        }
        return SymmetricKey(data: Data(derived))
    }

    // MARK: - Tar plumbing

    /// Wraps `directory` (e.g. `~/Library/Application Support/Hygur`) into
    /// a gzipped tarball whose root entry is the directory's basename.
    private static func tarGzip(directory: URL) async throws -> URL {
        let parent = directory.deletingLastPathComponent()
        let basename = directory.lastPathComponent
        let outFile = FileManager.default.temporaryDirectory
            .appendingPathComponent("hygur-archive-\(UUID().uuidString).tar.gz")

        try await runTar(args: [
            "--no-mac-metadata", // strip resource forks/xattrs that bloat archive
            "-czf", outFile.path,
            "-C", parent.path,
            basename,
        ])

        return outFile
    }

    private static func tarExtract(archive: URL, into dest: URL) async throws {
        try await runTar(args: ["-xzf", archive.path, "-C", dest.path])
    }

    private static func runTar(args: [String]) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            let proc = Process()
            proc.executableURL = URL(fileURLWithPath: "/usr/bin/tar")
            proc.arguments = args
            let errPipe = Pipe()
            proc.standardError = errPipe
            proc.standardOutput = Pipe()

            proc.terminationHandler = { p in
                if p.terminationStatus == 0 {
                    continuation.resume()
                } else {
                    let stderr = (try? errPipe.fileHandleForReading.readToEnd()) ?? Data()
                    let msg = String(data: stderr, encoding: .utf8) ?? "tar exit \(p.terminationStatus)"
                    continuation.resume(throwing: BackupError.archiveFailed(msg.trimmingCharacters(in: .whitespacesAndNewlines)))
                }
            }

            do {
                try proc.run()
            } catch {
                continuation.resume(throwing: BackupError.archiveFailed(error.localizedDescription))
            }
        }
    }

    // MARK: - Filenames

    private static func filenameTimestamp(_ date: Date) -> String {
        let formatter = DateFormatter()
        formatter.dateFormat = "yyyy-MM-dd-HHmmss"
        return formatter.string(from: date)
    }

    static func defaultExportFilename(date: Date = Date()) -> String {
        return "Hygur-\(filenameTimestamp(date)).hygurbackup"
    }
}
