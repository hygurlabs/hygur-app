import SwiftUI

/// `MenubarStatus` is a coarse health indicator combining sidecar reachability
/// and LM Studio reachability into a single traffic-light color.
enum MenubarStatus {
    case green   // sidecar connected AND lm_studio == .up
    case orange  // sidecar connected, lm_studio unknown or transitioning
    case red     // sidecar disconnected OR lm_studio == .down

    var color: Color {
        switch self {
        case .green: return HygurColors.success
        case .orange: return HygurColors.warning
        case .red: return HygurColors.danger
        }
    }

    var systemImage: String { "circle.fill" }

    var accessibilityLabel: String {
        switch self {
        case .green: return "Hygur connected"
        case .orange: return "Hygur partially available"
        case .red: return "Hygur offline"
        }
    }
}

/// `MenubarStatusIcon` renders the small traffic-light icon used as the
/// macOS menubar `MenubarExtra` label. Reads from `EventStreamService` to
/// stay reactive without polling.
struct MenubarStatusIcon: View {
    @Environment(EventStreamService.self) private var events

    var body: some View {
        Image(systemName: status.systemImage)
            .foregroundStyle(status.color)
            .accessibilityLabel(status.accessibilityLabel)
    }

    private var status: MenubarStatus {
        if !events.sidecarConnected { return .red }
        switch events.lmStudioStatus {
        case .up: return .green
        case .down: return .red
        case .unknown: return .orange
        }
    }
}
