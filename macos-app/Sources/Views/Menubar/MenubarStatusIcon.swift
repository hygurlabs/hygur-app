import SwiftUI

/// Coarse health states for the menu bar indicator. Distinct shapes — not
/// just colors — so users can tell at a glance whether the bundled sidecar
/// process is offline (the app itself is broken until we recover) versus
/// just the AI runtime being unreachable (chat replies fail, but everything
/// else still works).
enum MenubarStatus {
    /// Sidecar is up and the AI runtime answered the last health probe.
    case allOK
    /// Sidecar is up but the runtime responded with an error or hasn't been
    /// reached yet. Most of the app keeps working — only LLM-backed surfaces
    /// (chat reply, brief, rerank) degrade.
    case runtimeOffline
    /// Sidecar is up but the runtime status is unknown (probe in flight or
    /// no flip event yet). Treat as transient.
    case runtimeUnknown
    /// Sidecar process or its SSE stream is unreachable. Hard fail — the app
    /// retries with backoff, but the user may want to hit "Reconnect now".
    case sidecarOffline

    var color: Color {
        switch self {
        case .allOK:           return HygurColors.success
        case .runtimeOffline,
             .runtimeUnknown:  return HygurColors.warning
        case .sidecarOffline:  return HygurColors.danger
        }
    }

    /// Distinct SF Symbols per state — avoids relying on color alone, which
    /// would be invisible in monochrome menu bar themes and to color-blind
    /// users. The symbols are also readable at the 16pt menu bar size.
    var systemImage: String {
        switch self {
        case .allOK:           return "sparkles"
        case .runtimeUnknown:  return "circle.dashed"
        case .runtimeOffline:  return "cpu"
        case .sidecarOffline:  return "exclamationmark.triangle.fill"
        }
    }

    var accessibilityLabel: String {
        switch self {
        case .allOK:           return "Hygur connected"
        case .runtimeUnknown:  return "Checking AI runtime"
        case .runtimeOffline:  return "AI runtime offline"
        case .sidecarOffline:  return "Hygur sidecar offline"
        }
    }

    /// Single-line tooltip shown on hover. Same copy lives in the panel
    /// header subtitle so the source of truth is one place if we tweak it.
    var tooltip: String {
        switch self {
        case .allOK:           return "Hygur · ready"
        case .runtimeUnknown:  return "Hygur · checking AI runtime…"
        case .runtimeOffline:  return "Hygur · AI runtime offline (chat replies disabled)"
        case .sidecarOffline:  return "Hygur · sidecar offline (retrying…)"
        }
    }
}

/// Renders the `MenuBarExtra` label as a small status glyph. Reads from
/// `EventStreamService` so it stays reactive without polling.
struct MenubarStatusIcon: View {
    @Environment(EventStreamService.self) private var events

    var body: some View {
        Image(systemName: status.systemImage)
            .foregroundStyle(status.color)
            .accessibilityLabel(status.accessibilityLabel)
            .help(status.tooltip)
    }

    private var status: MenubarStatus {
        if !events.sidecarConnected { return .sidecarOffline }
        switch events.lmStudioStatus {
        case .up:      return .allOK
        case .down:    return .runtimeOffline
        case .unknown: return .runtimeUnknown
        }
    }
}
