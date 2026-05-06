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
    ///
    /// Note: chmod is intentionally NOT done here. This is a non-mutating computed
    /// property and cannot write to `lastError`. The execute-bit fix is applied in
    /// `start()` before the `isExecutableFile` check so errors are properly surfaced.
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

    /// Health-probe state — used by Settings to render the status row without
    /// flicker. The supervisor probes `/health` every 5 s once started and
    /// only flips `isHealthy` to `false` after `unhealthyThreshold` consecutive
    /// failures, so a single dropped packet doesn't paint the dot red.
    private(set) var isHealthy: Bool = false
    private var healthFailureStreak: Int = 0
    private let unhealthyThreshold: Int = 2
    private var healthTask: Task<Void, Never>?

    /// Spawn the sidecar. No-op if already running. Sets `lastError` if the
    /// binary is missing or cannot be made executable.
    func start() {
        guard !isRunning else { return }
        intentionalStop = false

        // Xcode's CpResource phase strips the execute bit from bundled binaries.
        // Restore it here (in a mutating context) so `isExecutableFile` passes.
        // If chmod fails, surface a precise error rather than the generic
        // "binary not found" message.
        let path = binaryPath
        let fm = FileManager.default
        if let attrs = try? fm.attributesOfItem(atPath: path.path),
           let perms = attrs[.posixPermissions] as? Int,
           perms & 0o111 == 0 {
            do {
                try fm.setAttributes(
                    [.posixPermissions: NSNumber(value: perms | 0o755)],
                    ofItemAtPath: path.path
                )
            } catch {
                lastError = "Cannot make sidecar binary executable: \(error.localizedDescription)"
                return
            }
        }

        guard fm.isExecutableFile(atPath: path.path) else {
            lastError = "Sidecar binary not found. In development, run `make install` in the sidecar repo."
            return
        }
        lastError = nil

        let proc = Process()
        proc.executableURL = path

        // Pipe stdout/stderr to the rotating log file. We open in append mode
        // so multiple respawns share one log; truncation is left to log rotation.
        if !FileManager.default.fileExists(atPath: logPath.path) {
            FileManager.default.createFile(atPath: logPath.path, contents: nil)
        }
        if let handle = try? FileHandle(forWritingTo: logPath) {
            var seekSucceeded = false
            do {
                try handle.seekToEnd()
                seekSucceeded = true
            } catch {
                // seekToEnd failed — closing the handle avoids writing at offset 0,
                // which would corrupt previously-written log content. Continue without
                // a log handle; stdout/stderr fall back to the parent process.
                try? handle.close()
            }
            if seekSucceeded {
                self.logHandle = handle
                proc.standardOutput = handle
                proc.standardError = handle
            }
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
            self.isHealthy = false
            self.healthFailureStreak = 0
            armStableTimer()
            startHealthPolling()
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
        healthTask?.cancel()
        healthTask = nil
        isHealthy = false
        healthFailureStreak = 0
        try? logHandle?.close()
        logHandle = nil
        process = nil
        pid = nil
        startedAt = nil
        isRunning = false
    }

    /// Periodic health probe. Called once after `start()`. Hits `/health` on
    /// the configured URL; flips `isHealthy` to true on the first success and
    /// only back to false after `unhealthyThreshold` consecutive failures.
    /// This is the single source of truth for the "sidecar reachable" badge —
    /// callers should prefer it over running their own probes, which is what
    /// caused the KO/OK/KO flicker the user reported.
    private func startHealthPolling() {
        healthTask?.cancel()
        let initialDelay: UInt64 = 500_000_000   // 0.5 s — give the server time to bind.
        let interval: UInt64 = 5_000_000_000     // 5 s.
        healthTask = Task { @MainActor [weak self] in
            try? await Task.sleep(nanoseconds: initialDelay)
            while let self, self.isRunning, !Task.isCancelled {
                let ok = await Self.probeHealth()
                if ok {
                    self.healthFailureStreak = 0
                    if !self.isHealthy { self.isHealthy = true }
                } else {
                    self.healthFailureStreak += 1
                    if self.healthFailureStreak >= self.unhealthyThreshold, self.isHealthy {
                        self.isHealthy = false
                    }
                }
                try? await Task.sleep(nanoseconds: interval)
            }
        }
    }

    /// One-shot health probe used by `startHealthPolling`. Built without
    /// touching `SidecarService` to keep the supervisor self-contained and
    /// avoid pulling auth headers into the loop — `/health` is unauthenticated.
    private nonisolated static func probeHealth() async -> Bool {
        let urlString = AppPreferences.shared.sidecarURL
        guard let base = URL(string: urlString) else { return false }
        var request = URLRequest(url: base.appendingPathComponent("health"))
        request.httpMethod = "GET"
        request.timeoutInterval = 3
        do {
            let (_, response) = try await URLSession.shared.data(for: request)
            guard let http = response as? HTTPURLResponse else { return false }
            return (200...299).contains(http.statusCode)
        } catch {
            return false
        }
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
