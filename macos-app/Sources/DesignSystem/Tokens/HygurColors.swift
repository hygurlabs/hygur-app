import SwiftUI
import AppKit

// MARK: - Dynamic color helper

extension Color {
    /// Builds a SwiftUI `Color` whose value depends on the active appearance.
    /// Use this for branded surfaces and accent shades that should differ
    /// between light and dark instead of relying on macOS system colors.
    static func dynamic(light: NSColor, dark: NSColor) -> Color {
        Color(nsColor: NSColor(name: nil) { appearance in
            let isDark = appearance.bestMatch(from: [.darkAqua, .vibrantDark, .accessibilityHighContrastDarkAqua, .accessibilityHighContrastVibrantDark]) != nil
            return isDark ? dark : light
        })
    }

    /// Convenience for hex literals like `#1F2937`. Alpha defaults to 1.
    static func dynamic(lightHex: UInt32, darkHex: UInt32, alpha: CGFloat = 1) -> Color {
        dynamic(light: NSColor(hex: lightHex, alpha: alpha), dark: NSColor(hex: darkHex, alpha: alpha))
    }
}

private extension NSColor {
    convenience init(hex: UInt32, alpha: CGFloat = 1) {
        let r = CGFloat((hex >> 16) & 0xFF) / 255
        let g = CGFloat((hex >> 8)  & 0xFF) / 255
        let b = CGFloat( hex        & 0xFF) / 255
        self.init(srgbRed: r, green: g, blue: b, alpha: alpha)
    }
}

// MARK: - Tokens

/// Semantic color tokens for Hygur. Surfaces and brand shades use explicit
/// light/dark values via `Color.dynamic`; text colors stay on SwiftUI's
/// `.primary`/`.secondary`/`.tertiary` so they keep respecting accessibility
/// preferences (increased contrast, reduce transparency).
enum HygurColors {
    // MARK: Surfaces

    /// Window background — the deepest layer.
    static var background: Color {
        .dynamic(lightHex: 0xF7F7F8, darkHex: 0x111114)
    }
    /// Default card / panel surface (sits on top of `background`).
    static var surface: Color {
        .dynamic(lightHex: 0xFFFFFF, darkHex: 0x1B1B20)
    }
    /// Raised surface for popovers, sheets, hover states.
    static var surfaceElevated: Color {
        .dynamic(lightHex: 0xFAFAFB, darkHex: 0x24242B)
    }

    /// Layered surface aliases — useful when stacking three depths in one
    /// view (e.g. timeline cards inside a panel inside the window).
    static var surface1: Color { background }
    static var surface2: Color { surface }
    static var surface3: Color { surfaceElevated }

    /// 1-pixel hairline separator. Subtle in light mode, slightly stronger
    /// in dark mode so it stays visible against denser surfaces.
    static var border: Color {
        .dynamic(light: NSColor.black.withAlphaComponent(0.10),
                 dark:  NSColor.white.withAlphaComponent(0.12))
    }

    // MARK: Content

    static var textPrimary: Color { .primary }
    static var textSecondary: Color { .secondary }
    static var textTertiary: Color { Color(nsColor: .tertiaryLabelColor) }

    // MARK: Brand & semantic

    /// System accent — respects the user's accent picked in System Settings.
    static var accent: Color { .accentColor }

    static var success: Color {
        .dynamic(lightHex: 0x1B873F, darkHex: 0x4ADE80)
    }
    static var warning: Color {
        .dynamic(lightHex: 0xB45309, darkHex: 0xFBBF24)
    }
    static var danger: Color {
        .dynamic(lightHex: 0xB91C1C, darkHex: 0xF87171)
    }

    // MARK: Source-type mapping

    /// Centralized mapping for the `source_type` enum used across views.
    /// Returns explicit light/dark values so the badge stays legible on both
    /// surfaces. Unknown types fall back to `.secondary`.
    static func sourceTypeColor(_ type: String) -> Color {
        switch type {
        case "markdown":               return .dynamic(lightHex: 0x1D4ED8, darkHex: 0x60A5FA)
        case "pdf":                    return .dynamic(lightHex: 0xB91C1C, darkHex: 0xF87171)
        case "docx", "doc":            return .dynamic(lightHex: 0x4338CA, darkHex: 0x818CF8)
        case "txt":                    return .dynamic(lightHex: 0x4B5563, darkHex: 0x9CA3AF)
        case "html":                   return .dynamic(lightHex: 0xC2410C, darkHex: 0xFB923C)
        case "note":                   return .dynamic(lightHex: 0xA16207, darkHex: 0xFBBF24)
        case "email", "thread", "mail":return .dynamic(lightHex: 0x0E7490, darkHex: 0x22D3EE)
        case "image":                  return .dynamic(lightHex: 0x7C3AED, darkHex: 0xA78BFA)
        case "audio":                  return .dynamic(lightHex: 0x0369A1, darkHex: 0x38BDF8)
        default:                       return .secondary
        }
    }

    static func sourceTypeIcon(_ type: String) -> String {
        switch type {
        case "markdown":                return "doc.text"
        case "pdf":                     return "doc.richtext"
        case "docx", "doc":             return "doc.fill"
        case "txt":                     return "doc.plaintext"
        case "html":                    return "globe"
        case "note":                    return "note.text"
        case "email", "thread", "mail": return "envelope"
        case "image":                   return "photo"
        case "audio":                   return "waveform"
        default:                        return "doc"
        }
    }

    // MARK: Gradients & hover states

    /// Subtle gradient from accent to transparent — used in card headers.
    static var accentGradient: LinearGradient {
        LinearGradient(
            colors: [accent.opacity(0.12), accent.opacity(0)],
            startPoint: .top,
            endPoint: .bottom
        )
    }

    /// Background color for hovered list rows.
    static func hoveredBackground(_ isHovered: Bool) -> Color {
        isHovered ? Color.primary.opacity(0.06) : Color.clear
    }

    // MARK: Connector status

    static func connectorStatusColor(_ status: String) -> Color {
        switch status {
        case "healthy":  return success
        case "degraded": return warning
        case "unhealthy":return danger
        default:         return .secondary
        }
    }
}
