import Foundation
import SwiftUI

/// Application settings with @AppStorage persistence
final class AppPreferences: ObservableObject {
    nonisolated(unsafe) static let shared = AppPreferences()

    @AppStorage("sidecarURL") var sidecarURL: String = "http://localhost:8420"
    @AppStorage("defaultModel") var defaultModel: String = ""
    @AppStorage("timeout") var timeout: Double = 120.0
    @AppStorage("theme") var theme: String = "system"

    private init() {}

    /// Validates the sidecar URL format
    var isValidURL: Bool {
        guard let url = URL(string: sidecarURL) else { return false }
        guard let scheme = url.scheme?.lowercased() else { return false }
        return (scheme == "http" || scheme == "https") && url.host != nil
    }

    /// Returns the sidecar URL as a URL object, or nil if invalid
    var sidecarURLValue: URL? {
        guard isValidURL else { return nil }
        return URL(string: sidecarURL)
    }
}

// MARK: - Bundle Extension

extension Bundle {
    var appVersion: String {
        (infoDictionary?["CFBundleShortVersionString"] as? String) ?? "0.0.0"
    }

    var buildNumber: String {
        (infoDictionary?["CFBundleVersion"] as? String) ?? "0"
    }
}
