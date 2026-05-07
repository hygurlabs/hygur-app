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
    // MARK: Brand

    /// Hygur primary brand blue — used for accents, selection, links, primary CTAs.
    static var brandBlue: Color {
        .dynamic(lightHex: 0x1D4ED8, darkHex: 0x60A5FA)
    }
    /// Subtle tint of brand blue — for selected row backgrounds, badges, hover.
    static var brandBlueSubtle: Color {
        .dynamic(light: NSColor(hex: 0x1D4ED8, alpha: 0.10),
                 dark:  NSColor(hex: 0x60A5FA, alpha: 0.18))
    }
    /// Hygur secondary brand gold — used for favorites, highlights, warm accents.
    static var brandGold: Color {
        .dynamic(lightHex: 0xC99A3F, darkHex: 0xF4C45F)
    }
    /// Subtle tint of brand gold — for favorite row backgrounds, soft highlights.
    static var brandGoldSubtle: Color {
        .dynamic(light: NSColor(hex: 0xC99A3F, alpha: 0.12),
                 dark:  NSColor(hex: 0xF4C45F, alpha: 0.18))
    }

    // MARK: Surfaces

    /// Window canvas — clear so the window's Liquid Glass material shows through.
    /// Views that need an opaque fallback (snapshots, exports) should use
    /// `surface` instead.
    static var background: Color { .clear }
    /// Default card / panel surface — sits on top of the window glass.
    /// Solid white / raised dark so card text stays legible against translucent chrome.
    static var surface: Color {
        .dynamic(lightHex: 0xFFFFFF, darkHex: 0x1B1B20)
    }
    /// Raised surface for popovers, sheets, hover states.
    static var surfaceElevated: Color {
        .dynamic(lightHex: 0xFAFAFB, darkHex: 0x24242B)
    }
    /// Sidebar background — clear so the native macOS sidebar Material renders.
    /// Painting an opaque color here would defeat Liquid Glass on macOS 26.
    static var sidebarBackground: Color { .clear }
    /// Status bar background — clear so the consumer can layer a `.thinMaterial`.
    static var statusBarBackground: Color { .clear }

    /// Layered surface aliases — useful when stacking three depths in one
    /// view (e.g. timeline cards inside a panel inside the window).
    /// `surface1` is opaque (vs. clear `background`) so views relying on a
    /// solid base — diff snapshots, export rendering — keep working.
    static var surface1: Color {
        .dynamic(lightHex: 0xFAFAFA, darkHex: 0x111114)
    }
    static var surface2: Color { surface }
    static var surface3: Color { surfaceElevated }

    /// 1-pixel hairline separator. Subtle in light mode, slightly stronger
    /// in dark mode so it stays visible against denser surfaces.
    static var border: Color {
        .dynamic(light: NSColor.black.withAlphaComponent(0.10),
                 dark:  NSColor.white.withAlphaComponent(0.12))
    }
    /// Alias for `border` — preferred at call sites that mean "section divider".
    static var divider: Color { border }

    // MARK: Content

    static var textPrimary: Color { .primary }
    static var textSecondary: Color { .secondary }
    static var textTertiary: Color { Color(nsColor: .tertiaryLabelColor) }

    // MARK: Brand & semantic

    /// Hygur accent — brand blue. Replaces the previous system-accent default
    /// so the app has a stable identity regardless of the user's macOS accent.
    static var accent: Color { brandBlue }

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
        case "markdown":               return brandBlue
        case "pdf":                    return .dynamic(lightHex: 0xB91C1C, darkHex: 0xF87171)
        case "docx", "doc":            return .dynamic(lightHex: 0x4338CA, darkHex: 0x818CF8)
        case "txt":                    return .dynamic(lightHex: 0x4B5563, darkHex: 0x9CA3AF)
        case "html":                   return .dynamic(lightHex: 0xC2410C, darkHex: 0xFB923C)
        case "note":                   return brandGold
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
