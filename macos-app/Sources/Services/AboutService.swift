import AppKit
import Foundation

/// Builds and shows the native About panel using `orderFrontStandardAboutPanel`
/// with a custom credits string. Keeping the macOS-standard panel (vs. a
/// bespoke window) means the user gets the familiar layout, app icon,
/// version line, and copyright field "for free" — we only contribute the
/// credits paragraph (clickable links to the repo and the author).
enum AboutService {
    @MainActor
    static func show() {
        NSApp.activate(ignoringOtherApps: true)
        NSApp.orderFrontStandardAboutPanel(options: [
            .credits: makeCredits(),
            .applicationName: "Hygur",
            .applicationVersion: Bundle.main.appVersion,
            .version: "Build \(Bundle.main.buildNumber)",
        ])
    }

    /// The credits NSAttributedString accepts inline links via the
    /// `.link` attribute — clicking opens them in the default browser.
    /// Two short paragraphs: a one-line tech blurb so the user knows what
    /// runs locally, then the personal "crafted by" line that doubles as
    /// a public attribution.
    private static func makeCredits() -> NSAttributedString {
        let body = NSMutableAttributedString()

        let bodyAttrs: [NSAttributedString.Key: Any] = [
            .font: NSFont.systemFont(ofSize: 11),
            .foregroundColor: NSColor.labelColor,
        ]
        let secondaryAttrs: [NSAttributedString.Key: Any] = [
            .font: NSFont.systemFont(ofSize: 11),
            .foregroundColor: NSColor.secondaryLabelColor,
        ]

        body.append(NSAttributedString(
            string: "Built with SwiftUI on macOS, a Go sidecar for ingestion and RAG, "
                + "and your own local AI runtime (vLLM, LM Studio, Ollama, …).\n\n",
            attributes: bodyAttrs
        ))

        body.append(NSAttributedString(string: "Crafted by ", attributes: bodyAttrs))
        body.append(linkAttributedString(text: "0x0800.com", url: "https://0x0800.com"))
        body.append(NSAttributedString(string: "  ·  ", attributes: secondaryAttrs))
        body.append(linkAttributedString(text: "GitHub", url: "https://github.com/hygurlabs/hygur"))

        let paragraph = NSMutableParagraphStyle()
        paragraph.alignment = .center
        paragraph.lineSpacing = 2
        body.addAttribute(.paragraphStyle,
                          value: paragraph,
                          range: NSRange(location: 0, length: body.length))
        return body
    }

    private static func linkAttributedString(text: String, url: String) -> NSAttributedString {
        let attrs: [NSAttributedString.Key: Any] = [
            .font: NSFont.systemFont(ofSize: 11, weight: .medium),
            .foregroundColor: NSColor.linkColor,
            .link: URL(string: url) as Any,
        ]
        return NSAttributedString(string: text, attributes: attrs)
    }
}
