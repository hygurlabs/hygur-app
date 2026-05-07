import SwiftUI

enum HygurTypography {
    static var largeTitle: Font { .largeTitle }
    static var title: Font { .title }
    static var title2: Font { .title2 }
    static var title3: Font { .title3 }
    static var headline: Font { .headline }
    static var body: Font { .body }
    static var callout: Font { .callout }
    static var subheadline: Font { .subheadline }
    static var footnote: Font { .footnote }
    static var caption: Font { .caption }
    static var captionMono: Font { .caption.monospaced() }

    // MARK: - Cards

    /// Card header title (e.g. note title in a list card).
    static var cardTitle: Font { .system(size: 13, weight: .semibold) }
    /// Card metadata line (timestamp, type, count).
    static var cardMeta: Font { .system(size: 11, weight: .regular) }

    // MARK: - Sidebar

    /// Uppercase section label in the sidebar (e.g. "FAVORITES").
    static var sidebarSection: Font { .system(size: 10, weight: .semibold).smallCaps() }
    /// Sidebar row label.
    static var sidebarItem: Font { .system(size: 12, weight: .medium) }

    // MARK: - Status bar

    /// Status bar caption — short, tight, low-contrast.
    static var statusCaption: Font { .system(size: 11, weight: .regular) }
}
