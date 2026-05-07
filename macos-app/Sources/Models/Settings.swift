import Foundation
import SwiftUI

/// Application settings with @AppStorage persistence
final class AppPreferences: ObservableObject {
    nonisolated(unsafe) static let shared = AppPreferences()

    @AppStorage("sidecarURL") var sidecarURL: String = "http://localhost:8420"
    @AppStorage("defaultModel") var defaultModel: String = ""
    @AppStorage("timeout") var timeout: Double = 120.0
    @AppStorage("theme") var theme: String = "system"
    /// Stored as the raw value of `QuickLookShortcut`. Read via
    /// `quickLookShortcut` for the typed accessor.
    @AppStorage("hygur.shortcut.quickLook") var quickLookShortcutRaw: String = QuickLookShortcut.space.rawValue

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

// MARK: - QuickLook Shortcut

/// User-configurable keyboard shortcut for opening the QuickLook preview from
/// list views (KB, Notes, etc.). Persisted via `AppPreferences`.
enum QuickLookShortcut: String, CaseIterable, Identifiable {
    case space
    case `return`
    case shiftSpace

    var id: String { rawValue }

    var label: String {
        switch self {
        case .space:      return "Space"
        case .return:     return "Return"
        case .shiftSpace: return "⇧ Space"
        }
    }

    /// Matches the `KeyEquivalent` reported by `.onKeyPress`.
    var keyEquivalent: KeyEquivalent {
        switch self {
        case .space, .shiftSpace: return .space
        case .return:             return .return
        }
    }

    /// When true, the matcher requires the Shift modifier alongside the key.
    var requiresShift: Bool { self == .shiftSpace }
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
