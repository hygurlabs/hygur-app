import Foundation
import Observation

/// `SidecarSupervisor` runs the Hygur sidecar binary as a child process of
/// the macOS app. It restarts the sidecar with exponential backoff if it
/// exits unexpectedly, and pipes stdout/stderr to a rotating log file.
///
/// The binary is searched at `~/.hygur/bin/hygur` (installed via the
/// sidecar repo's `make install`). When it's missing, the supervisor stays
/// idle and surfaces an error message — the user can still talk to a
/// manually-launched remote sidecar via the URL configured in Settings.
@MainActor
@Observable
final class SidecarSupervisor {
    /// Whether the supervised process is currently running.
    private(set) var isRunning: Bool = false
    /// Last error encountered when starting the binary, surfaced in Settings.
    private(set) var lastError: String?
    /// Process identifier of the running sidecar, for the Settings status row.
    private(set) var pid: Int32?
    /// When the current child started; used to compute uptime in the UI.
    private(set) var startedAt: Date?

    /// Backoff schedule between unexpected exits. Reset to 0 after 60 s of
    /// stable run.
    private let backoff: [TimeInterval] = [1, 5, 30]
    private var backoffIndex = 0

    private var process: Process?
    private var logHandle: FileHandle?
    private var stableTimer: Timer?
    private var intentionalStop = false

    /// Path to the sidecar binary.
    ///
    /// Resolution order:
    /// 1. Bundled resource inside the .app (release builds) — `hygur-sidecar`
    /// 2. Development fallback — `~/.hygur/bin/hygur` (installed via `make install`)
    var binaryPath: URL {
        if let bundled = Bundle.main.url(forResource: "hygur-sidecar", withExtension: nil) {
            return bundled
        }
        return FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".hygur/bin/hygur")
    }

    /// Path to the sidecar log file inside ~/Library/Logs/Hygur.
    var logPath: URL {
        let home = FileManager.default.homeDirectoryForCurrentUser
        let dir = home.appendingPathComponent("Library/Logs/Hygur", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir.appendingPathComponent("sidecar.log")
    }

    /// Spawn the sidecar. No-op if already running. Sets `lastError` if the
    /// binary is missing.
    func start() {
        guard !isRunning else { return }
        intentionalStop = false

        guard FileManager.default.isExecutableFile(atPath: binaryPath.path) else {
            lastError = "Sidecar binary not found. In development, run `make install` in the sidecar repo."
            return
        }
        lastError = nil

        let proc = Process()
        proc.executableURL = binaryPath

        // Pipe stdout/stderr to the rotating log file. We open in append mode
        // so multiple respawns share one log; truncation is left to log rotation.
        if !FileManager.default.fileExists(atPath: logPath.path) {
            FileManager.default.createFile(atPath: logPath.path, contents: nil)
        }
        if let handle = try? FileHandle(forWritingTo: logPath) {
            _ = try? handle.seekToEnd()
            self.logHandle = handle
            proc.standardOutput = handle
            proc.standardError = handle
        }

        proc.terminationHandler = { [weak self] terminated in
            Task { @MainActor [weak self] in
                self?.handleTermination(terminated)
            }
        }

        do {
            try proc.run()
            self.process = proc
            self.pid = proc.processIdentifier
            self.startedAt = Date()
            self.isRunning = true
            armStableTimer()
        } catch {
            lastError = "Failed to launch sidecar: \(error.localizedDescription)"
        }
    }

    /// SIGTERM the sidecar; if it doesn't exit within 5 s, escalate to SIGKILL.
    func stop() async {
        guard let proc = process else { return }
        intentionalStop = true
        proc.terminate()
        // Wait up to 5 seconds for graceful exit
        for _ in 0..<50 {
            if !proc.isRunning { break }
            try? await Task.sleep(nanoseconds: 100_000_000)
        }
        if proc.isRunning {
            kill(proc.processIdentifier, SIGKILL)
        }
        cleanup()
    }

    /// Stop and immediately restart — used by the "Restart sidecar" button.
    func restart() async {
        await stop()
        try? await Task.sleep(nanoseconds: 200_000_000)
        start()
    }

    /// Uptime of the current child process, or nil if not running.
    var uptime: TimeInterval? {
        guard isRunning, let started = startedAt else { return nil }
        return Date().timeIntervalSince(started)
    }

    // MARK: - Private

    private func handleTermination(_ terminated: Process) {
        cleanup()
        if intentionalStop { return }

        // Unexpected exit — schedule a respawn after backoff.
        let wait = backoff[min(backoffIndex, backoff.count - 1)]
        backoffIndex = min(backoffIndex + 1, backoff.count - 1)
        Task { @MainActor [weak self] in
            try? await Task.sleep(nanoseconds: UInt64(wait * 1_000_000_000))
            self?.start()
        }
    }

    private func cleanup() {
        stableTimer?.invalidate()
        stableTimer = nil
        try? logHandle?.close()
        logHandle = nil
        process = nil
        pid = nil
        startedAt = nil
        isRunning = false
    }

    /// After 60 s of stable run, reset the backoff counter so a future crash
    /// gets the fast retry treatment again.
    private func armStableTimer() {
        stableTimer?.invalidate()
        stableTimer = Timer.scheduledTimer(withTimeInterval: 60, repeats: false) { [weak self] _ in
            Task { @MainActor [weak self] in
                self?.backoffIndex = 0
            }
        }
    }
}
