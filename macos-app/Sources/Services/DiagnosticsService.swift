import Foundation
import AppKit
import AVFoundation
import Speech
import EventKit
import UserNotifications

/// Builds an opt-in diagnostics report (no PII beyond paths under the user's
/// home dir) the user can copy from Settings → System → Support and paste into
/// a GitHub issue. The point is to short-circuit the back-and-forth where a
/// tester reports "it doesn't work" without enough context to triage.
///
/// Anything we collect must be safe to share publicly: app/sidecar versions,
/// runtime reachability, permission grants (yes/no, never the data itself),
/// and rough storage size. We deliberately do **not** include log file
/// contents — too easy to leak email subjects or doc titles. The user can
/// attach those manually if needed.
enum DiagnosticsService {

    /// Public entry point. Pulls everything we can read locally plus a best-effort
    /// probe of the sidecar's `/healthz`. Always returns a usable report — if a
    /// probe fails we record that fact rather than throwing.
    @MainActor
    static func collect() async -> String {
        var sections: [String] = []

        sections.append(appSection())
        sections.append(systemSection())
        sections.append(await sidecarSection())
        sections.append(runtimeSection())
        sections.append(await permissionsSection())
        sections.append(storageSection())
        sections.append(preferencesSection())

        let header = """
        # Hygur Diagnostics
        Generated \(ISO8601DateFormatter().string(from: Date()))

        > Paste this in your GitHub issue. Review before sending — paths under your home directory are included.
        """

        return ([header] + sections).joined(separator: "\n\n")
    }

    /// Copy the report to the system pasteboard and return the byte count so
    /// the UI can surface a meaningful confirmation.
    @MainActor
    static func copyToClipboard() async -> (text: String, byteCount: Int) {
        let report = await collect()
        let pb = NSPasteboard.general
        pb.clearContents()
        pb.setString(report, forType: .string)
        return (report, report.utf8.count)
    }

    // MARK: - Sections

    private static func appSection() -> String {
        """
        ## Application
        - Version: \(Bundle.main.appVersion) (build \(Bundle.main.buildNumber))
        - Bundle ID: \(Bundle.main.bundleIdentifier ?? "—")
        """
    }

    private static func systemSection() -> String {
        let info = ProcessInfo.processInfo
        let os = info.operatingSystemVersion
        let locale = Locale.current.identifier
        let arch: String
        #if arch(arm64)
        arch = "arm64 (Apple Silicon)"
        #elseif arch(x86_64)
        arch = "x86_64 (Intel)"
        #else
        arch = "unknown"
        #endif
        return """
        ## System
        - macOS: \(os.majorVersion).\(os.minorVersion).\(os.patchVersion)
        - Architecture: \(arch)
        - Locale: \(locale)
        - Physical memory: \(formatBytes(Int64(info.physicalMemory)))
        """
    }

    private static func sidecarSection() async -> String {
        let svc = SidecarService.fromSettings()
        let baseURL = AppPreferences.shared.sidecarURL
        do {
            let h = try await svc.health()
            return """
            ## Sidecar
            - URL: \(baseURL)
            - Reachable: yes
            - Version: \(h.version)
            - Status: \(h.status)
            - Uptime: \(h.uptimeSeconds.map { "\($0) s" } ?? "—")
            - LM Studio probe: \(h.lmStudio)
            """
        } catch {
            return """
            ## Sidecar
            - URL: \(baseURL)
            - Reachable: no (\(error.localizedDescription))
            """
        }
    }

    /// Read-only snapshot of the runtime config the sidecar is using. Surfaces
    /// what the user actually configured (URLs, model IDs) without dumping the
    /// whole config — secrets stay out by construction since we never request
    /// them through this path.
    private static func runtimeSection() -> String {
        // We deliberately avoid awaiting another sidecar call here — sidecarSection
        // already covers liveness. The runtime URL is in Settings → Local LLM and
        // we'd rather keep the report deterministic when the sidecar is offline.
        let prefs = AppPreferences.shared
        return """
        ## Runtime
        - Sidecar URL: \(prefs.sidecarURL)
        - Default model: \(prefs.defaultModel.isEmpty ? "—" : prefs.defaultModel)
        - Timeout: \(Int(prefs.timeout)) s
        """
    }

    /// Best-effort permission grant snapshot. Only flags whether the user has
    /// granted the prompt — never reads the underlying data.
    private static func permissionsSection() async -> String {
        let mic = AVCaptureDevice.authorizationStatus(for: .audio).label
        let speech = SFSpeechRecognizer.authorizationStatus().label
        let calendar = EKEventStore.authorizationStatus(for: .event).label
        let notifications = await notificationsAuthorizationLabel()

        return """
        ## Permissions
        - Microphone: \(mic)
        - Speech recognition: \(speech)
        - Calendar: \(calendar)
        - Notifications: \(notifications)
        """
    }

    private static func notificationsAuthorizationLabel() async -> String {
        let settings = await UNUserNotificationCenter.current().notificationSettings()
        return settings.authorizationStatus.label
    }

    @MainActor
    private static func storageSection() -> String {
        let dir = BackupService.dataDirectoryURL
        let path = dir.path
        let exists = FileManager.default.fileExists(atPath: path)
        guard exists else {
            return """
            ## Storage
            - Data directory: \(path)
            - Exists: no (sidecar may not have run yet)
            """
        }
        let bytes = directorySize(at: dir)
        return """
        ## Storage
        - Data directory: \(path)
        - Size: \(formatBytes(bytes))
        """
    }

    /// Subset of `@AppStorage` keys that are safe to share — we exclude any
    /// key likely to hold a token or personally identifying free text. The
    /// rest gives someone triaging the issue an at-a-glance view of how the
    /// app is configured (hotkeys on/off, runtime mode, theme, etc.).
    private static func preferencesSection() -> String {
        let defaults = UserDefaults.standard
        let safeKeys: [String] = [
            "theme",
            "ui.menuBarOnly",
            "hotkey.summon.enabled",
            "hygur.shortcut.quickLook",
            "notify.dailyBrief",
            "notify.priorityMail",
            "onboarding.completed",
        ]
        let lines = safeKeys.map { key -> String in
            let value = defaults.object(forKey: key)
            let display: String
            switch value {
            case let b as Bool: display = b ? "true" : "false"
            case let s as String: display = s.isEmpty ? "(empty)" : s
            case let n as NSNumber: display = n.stringValue
            case .none: display = "(unset)"
            default: display = String(describing: value!)
            }
            return "- \(key): \(display)"
        }
        return """
        ## Preferences
        \(lines.joined(separator: "\n"))
        """
    }

    // MARK: - Helpers

    private static func directorySize(at url: URL) -> Int64 {
        guard let enumerator = FileManager.default.enumerator(
            at: url,
            includingPropertiesForKeys: [.totalFileAllocatedSizeKey, .isRegularFileKey],
            options: [.skipsHiddenFiles]
        ) else { return 0 }
        var total: Int64 = 0
        for case let fileURL as URL in enumerator {
            guard let values = try? fileURL.resourceValues(forKeys: [.totalFileAllocatedSizeKey, .isRegularFileKey]),
                  values.isRegularFile == true,
                  let size = values.totalFileAllocatedSize else { continue }
            total += Int64(size)
        }
        return total
    }

    private static func formatBytes(_ bytes: Int64) -> String {
        let f = ByteCountFormatter()
        f.allowedUnits = [.useMB, .useGB, .useKB]
        f.countStyle = .file
        return f.string(fromByteCount: bytes)
    }

    // MARK: - GitHub issue link

    /// Builds a prefilled GitHub issue URL with a body that contains the
    /// diagnostics report. We cap at ~6 KB so the URL stays under most
    /// browser limits; a longer report is referenced via "see clipboard".
    @MainActor
    static func makeIssueURL(report: String) -> URL? {
        let header = "Describe what you were doing and what went wrong:\n\n\n\n---\n\n"
        let footer = "\n\n<!-- Diagnostics report — review before submitting -->"
        let maxBody = 6000
        let body: String
        if report.count + header.count + footer.count <= maxBody {
            body = header + report + footer
        } else {
            let truncated = String(report.prefix(maxBody - header.count - footer.count - 80))
            body = header + truncated + "\n\n…[truncated — full report copied to clipboard]" + footer
        }
        var components = URLComponents(string: "https://github.com/hygurlabs/hygur/issues/new")
        components?.queryItems = [
            URLQueryItem(name: "title", value: "[Hygur \(Bundle.main.appVersion)] "),
            URLQueryItem(name: "body", value: body),
            URLQueryItem(name: "labels", value: "bug,from-app"),
        ]
        return components?.url
    }
}

// MARK: - Status labels

private extension AVAuthorizationStatus {
    var label: String {
        switch self {
        case .notDetermined: return "not asked"
        case .restricted:    return "restricted"
        case .denied:        return "denied"
        case .authorized:    return "granted"
        @unknown default:    return "unknown"
        }
    }
}

private extension SFSpeechRecognizerAuthorizationStatus {
    var label: String {
        switch self {
        case .notDetermined: return "not asked"
        case .restricted:    return "restricted"
        case .denied:        return "denied"
        case .authorized:    return "granted"
        @unknown default:    return "unknown"
        }
    }
}

private extension EKAuthorizationStatus {
    var label: String {
        switch self {
        case .notDetermined: return "not asked"
        case .restricted:    return "restricted"
        case .denied:        return "denied"
        case .fullAccess:    return "full access"
        case .writeOnly:     return "write-only"
        case .authorized:    return "granted (legacy)"
        @unknown default:    return "unknown"
        }
    }
}

private extension UNAuthorizationStatus {
    var label: String {
        switch self {
        case .notDetermined: return "not asked"
        case .denied:        return "denied"
        case .authorized:    return "granted"
        case .provisional:   return "provisional"
        case .ephemeral:     return "ephemeral"
        @unknown default:    return "unknown"
        }
    }
}
