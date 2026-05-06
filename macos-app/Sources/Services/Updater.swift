import AppKit
import CryptoKit
import Foundation
import Observation
import SwiftUI

enum UpdateStatus: Equatable {
    case idle
    case checking
    case upToDate
    case available(ReleaseInfo)
    case downloading(progress: Double)
    case readyToInstall(URL)
    case installing
    case error(String)
}

@MainActor
@Observable
final class Updater {
    private(set) var status: UpdateStatus = .idle
    private(set) var latestRelease: ReleaseInfo?

    @ObservationIgnored
    @AppStorage("update.autoCheckEnabled") private var autoCheckStorage: Bool = true
    @ObservationIgnored
    @AppStorage("update.lastCheckedAt") private var lastCheckedAtRaw: Double = 0

    /// Surfaced through a regular property so view code can observe changes.
    var autoCheckEnabled: Bool {
        get { autoCheckStorage }
        set { autoCheckStorage = newValue }
    }

    var lastCheckedAt: Date? {
        lastCheckedAtRaw == 0 ? nil : Date(timeIntervalSince1970: lastCheckedAtRaw)
    }

    private let client: GitHubReleasesClient

    init(client: GitHubReleasesClient = GitHubReleasesClient()) {
        self.client = client
    }

    // MARK: - Check

    /// Run at app launch. No-op if auto-check is disabled or the last check
    /// was less than 24 hours ago — keeps the network footprint minimal and
    /// respects the project's local-first ethos.
    func checkAtLaunchIfDue() async {
        guard autoCheckEnabled else { return }
        if let last = lastCheckedAt, Date().timeIntervalSince(last) < 24 * 3600 {
            // Still surface a previously-detected available update so the
            // sidebar/menu badge survives across restarts.
            if case .idle = status, let cached = latestRelease, isNewer(cached) {
                status = .available(cached)
            }
            return
        }
        await checkForUpdates()
    }

    /// Fetch the latest stable release and update status. Always updates
    /// `lastCheckedAt`, even on transient failures, to avoid hammering GitHub
    /// when the user is offline.
    func checkForUpdates() async {
        status = .checking
        defer { lastCheckedAtRaw = Date().timeIntervalSince1970 }

        do {
            let release = try await client.fetchLatestRelease()
            latestRelease = release
            if isNewer(release) {
                status = .available(release)
            } else {
                status = .upToDate
            }
        } catch let error as GitHubReleasesClient.ClientError {
            switch error {
            case .noStableRelease:
                status = .upToDate
            default:
                status = .error(error.localizedDescription)
            }
        } catch {
            status = .error(error.localizedDescription)
        }
    }

    // MARK: - Download & Install

    /// Drives the full download → verify → install flow. Idempotent against the
    /// current `status`: re-clicking after a successful download skips straight
    /// to install rather than re-fetching the DMG.
    func downloadAndInstall() async {
        if case .readyToInstall(let url) = status {
            await runInstaller(dmgURL: url)
            return
        }
        guard case .available(let release) = status,
              let asset = release.dmgAsset else {
            status = .error("No update available.")
            return
        }
        do {
            let dmgURL = try await downloadDMG(asset: asset)
            try verifySHA256(at: dmgURL, expected: asset.sha256Hex)
            status = .readyToInstall(dmgURL)
            await runInstaller(dmgURL: dmgURL)
        } catch {
            status = .error("Download failed: \(error.localizedDescription)")
        }
    }

    /// Download the DMG to a unique location in the system temp directory and
    /// stream `downloading(progress:)` events as bytes arrive. Uses the delegate
    /// API because `URLSession.download(for:)` doesn't surface progress.
    private func downloadDMG(asset: ReleaseAsset) async throws -> URL {
        status = .downloading(progress: 0)

        let coordinator = DownloadCoordinator(
            destinationDirectory: FileManager.default.temporaryDirectory,
            expectedSize: asset.size,
            onProgress: { [weak self] progress in
                Task { @MainActor [weak self] in
                    guard let self else { return }
                    if case .downloading = self.status {
                        self.status = .downloading(progress: progress)
                    }
                }
            }
        )

        let session = URLSession(configuration: .default, delegate: coordinator, delegateQueue: nil)
        defer { session.finishTasksAndInvalidate() }

        let task = session.downloadTask(with: asset.browserDownloadURL)
        return try await coordinator.start(task: task)
    }

    /// Verify the on-disk SHA256 against the digest GitHub returns for the
    /// asset. If GitHub didn't populate `digest` (older uploads), we just log
    /// and continue — there's nothing to compare against.
    private func verifySHA256(at url: URL, expected: String?) throws {
        guard let expected else {
            // No published digest — best-effort, skip verification.
            return
        }
        let actual = try Self.sha256Hex(of: url)
        if actual != expected {
            try? FileManager.default.removeItem(at: url)
            throw NSError(domain: "Updater", code: 1, userInfo: [
                NSLocalizedDescriptionKey: "The downloaded file's SHA256 hash does not match the published one."
            ])
        }
    }

    /// Streaming SHA256 — keeps memory usage flat regardless of DMG size.
    private static func sha256Hex(of url: URL) throws -> String {
        let handle = try FileHandle(forReadingFrom: url)
        defer { try? handle.close() }
        var hasher = SHA256()
        while true {
            let chunk = handle.readData(ofLength: 1_048_576) // 1 MiB
            if chunk.isEmpty { break }
            hasher.update(data: chunk)
        }
        return hasher.finalize().map { String(format: "%02x", $0) }.joined()
    }

    /// Spawn `install-update.sh` (bundled resource) detached from this process,
    /// then quit so the script can replace `/Applications/Hygur.app` and relaunch
    /// the new build. Falls back to opening the DMG in Finder if the app isn't
    /// running from `/Applications` (dev builds, manual installs in `~/Apps`, …).
    private func runInstaller(dmgURL: URL) async {
        status = .installing

        // Sanity check: we can only swap the bundle if it lives in /Applications.
        // The script also needs sudo-less write access to that directory; users
        // running from anywhere else get a graceful fallback.
        let bundlePath = Bundle.main.bundlePath
        let isInApplications = bundlePath.hasPrefix("/Applications/")
        guard isInApplications else {
            NSWorkspace.shared.open(dmgURL)
            status = .error("Hygur is not running from /Applications. The DMG has been opened in Finder — drag Hygur.app into /Applications manually.")
            return
        }

        guard let scriptURL = Bundle.main.url(forResource: "install-update", withExtension: "sh") else {
            status = .error("Installer script not found in bundle.")
            return
        }

        // Bundle resources are read-only and may live on a read-only volume.
        // Copy to a writable temp location, mark executable, then run.
        let tmpScript = FileManager.default.temporaryDirectory
            .appendingPathComponent("hygur-install-\(UUID().uuidString).sh")
        do {
            try FileManager.default.copyItem(at: scriptURL, to: tmpScript)
            try FileManager.default.setAttributes(
                [.posixPermissions: NSNumber(value: 0o755)],
                ofItemAtPath: tmpScript.path
            )

            let task = Process()
            task.executableURL = URL(fileURLWithPath: "/bin/bash")
            task.arguments = [
                tmpScript.path,
                String(ProcessInfo.processInfo.processIdentifier),
                dmgURL.path,
            ]
            // Detach stdin/stdout/stderr — once we exit, the parent's pipes
            // disappear and Process() would otherwise SIGPIPE its child.
            task.standardInput = FileHandle.nullDevice
            task.standardOutput = FileHandle.nullDevice
            task.standardError = FileHandle.nullDevice
            try task.run()
        } catch {
            status = .error("Could not launch the installer: \(error.localizedDescription)")
            return
        }

        // Give SwiftUI a tick to render the "installing" state before terminate
        // tears the window hierarchy down. The script polls our PID and waits
        // for us to actually exit before swapping the bundle.
        try? await Task.sleep(nanoseconds: 400_000_000)
        NSApplication.shared.terminate(nil)
    }

    // MARK: - Semver

    /// True if `release.version` is strictly greater than the bundle version.
    /// Compares dotted decimal components numerically — "0.1.10" beats "0.1.9".
    func isNewer(_ release: ReleaseInfo) -> Bool {
        Self.compare(release.version, Bundle.main.appVersion) == .orderedDescending
    }

    static func compare(_ a: String, _ b: String) -> ComparisonResult {
        let pa = a.split(separator: ".").map { Int($0) ?? 0 }
        let pb = b.split(separator: ".").map { Int($0) ?? 0 }
        let count = max(pa.count, pb.count)
        for i in 0..<count {
            let x = i < pa.count ? pa[i] : 0
            let y = i < pb.count ? pb[i] : 0
            if x != y { return x < y ? .orderedAscending : .orderedDescending }
        }
        return .orderedSame
    }
}
