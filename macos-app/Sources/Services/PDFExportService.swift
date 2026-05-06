import AppKit
import CoreText
import Foundation
import UniformTypeIdentifiers

/// Renders chat sessions and notes to PDF via `NSSavePanel`.
///
/// Strategy: reuse `MarkdownExportService.renderSession` / `renderNote` so
/// the textual content is identical to the .md export, parse the resulting
/// Markdown into an `AttributedString`, then paginate through CoreText
/// (`CTFramesetter`) so long conversations span multiple pages without
/// truncation. We deliberately avoid WebKit-based rendering — it would
/// pull in WKWebView for what is essentially a styled text dump.
@MainActor
enum PDFExportService {
    enum ExportError: LocalizedError {
        case userCancelled
        case write(Error)

        var errorDescription: String? {
            switch self {
            case .userCancelled: return nil
            case .write(let error): return "Could not write PDF: \(error.localizedDescription)"
            }
        }
    }

    // MARK: - Public API

    @discardableResult
    static func exportChatSession(_ session: ChatSession) throws -> URL {
        let markdown = MarkdownExportService.renderSession(session)
        let data = renderPDF(markdown: markdown, title: session.displayTitle)
        return try save(data: data, defaultName: defaultFilename(for: session))
    }

    @discardableResult
    static func exportNote(_ note: Note) throws -> URL {
        let markdown = MarkdownExportService.renderNote(note)
        let data = renderPDF(markdown: markdown, title: note.title)
        return try save(data: data, defaultName: defaultFilename(for: note))
    }

    // MARK: - Rendering

    private static let pageSize = CGSize(width: 612, height: 792) // US Letter, 72 dpi
    private static let margin: CGFloat = 54

    private static func renderPDF(markdown: String, title: String) -> Data {
        let attributed = parseMarkdown(markdown)

        let pdfData = NSMutableData()
        guard let consumer = CGDataConsumer(data: pdfData) else { return Data() }
        var mediaBox = CGRect(origin: .zero, size: pageSize)
        // Embed PDF metadata so Finder/Preview show the right title.
        let info: [CFString: Any] = [
            kCGPDFContextTitle: title,
            kCGPDFContextCreator: "Hygur",
        ]
        guard let ctx = CGContext(consumer: consumer, mediaBox: &mediaBox, info as CFDictionary) else {
            return Data()
        }

        let framesetter = CTFramesetterCreateWithAttributedString(attributed as CFAttributedString)
        let totalLength = CFAttributedStringGetLength(attributed as CFAttributedString)
        var currentRange = CFRange(location: 0, length: 0)

        let printable = CGRect(
            x: margin,
            y: margin,
            width: pageSize.width - 2 * margin,
            height: pageSize.height - 2 * margin
        )

        while currentRange.location < totalLength {
            ctx.beginPDFPage(nil)
            // Core Text draws with a bottom-left origin; flip so we can pass
            // a top-left rect like AppKit conventions.
            ctx.translateBy(x: 0, y: pageSize.height)
            ctx.scaleBy(x: 1, y: -1)

            let path = CGPath(
                rect: CGRect(
                    x: printable.minX,
                    y: pageSize.height - printable.maxY,
                    width: printable.width,
                    height: printable.height
                ),
                transform: nil
            )

            let frame = CTFramesetterCreateFrame(framesetter, currentRange, path, nil)
            CTFrameDraw(frame, ctx)

            let visible = CTFrameGetVisibleStringRange(frame)
            ctx.endPDFPage()

            if visible.length <= 0 { break } // safety net against infinite loop on a degenerate frame
            currentRange = CFRange(location: visible.location + visible.length, length: 0)
        }

        ctx.closePDF()
        return pdfData as Data
    }

    /// Parse Markdown into an AttributedString with paragraph-level
    /// interpretation, then promote heading and inline-code markers to
    /// visible styling (the system parser tags them but doesn't change
    /// fonts on macOS). Returns an NSAttributedString ready for CoreText.
    private static func parseMarkdown(_ markdown: String) -> NSAttributedString {
        let opts = AttributedString.MarkdownParsingOptions(
            allowsExtendedAttributes: true,
            interpretedSyntax: .full,
            failurePolicy: .returnPartiallyParsedIfPossible
        )
        let parsed: AttributedString
        if let attr = try? AttributedString(markdown: markdown, options: opts) {
            parsed = attr
        } else {
            parsed = AttributedString(markdown)
        }

        let mutable = NSMutableAttributedString(attributedString: NSAttributedString(parsed))
        applyBaseStyling(mutable)
        applyMarkdownStyling(mutable, source: parsed)
        return mutable
    }

    private static func applyBaseStyling(_ attr: NSMutableAttributedString) {
        let paragraph = NSMutableParagraphStyle()
        paragraph.lineSpacing = 2
        paragraph.paragraphSpacing = 6

        let baseFont = NSFont.systemFont(ofSize: 11)
        attr.addAttributes(
            [
                .font: baseFont,
                .paragraphStyle: paragraph,
                .foregroundColor: NSColor.textColor,
            ],
            range: NSRange(location: 0, length: attr.length)
        )
    }

    /// Walk the parsed AttributedString to find heading-level ranges and
    /// re-apply larger fonts on the corresponding NSAttributedString
    /// ranges. Inline code is kept as monospaced.
    private static func applyMarkdownStyling(_ ns: NSMutableAttributedString, source: AttributedString) {
        let plainNS = ns.string as NSString
        // AttributedString.distance is shadowed by attribute-key lookup, so we
        // route through the CharacterView and convert to NSRange via the
        // backing String (which gives correct UTF-16 offsets for emoji/accents).
        let baseString = String(source.characters)
        for run in source.runs {
            let charStart = source.characters.distance(from: source.startIndex, to: run.range.lowerBound)
            let charEnd = source.characters.distance(from: source.startIndex, to: run.range.upperBound)
            let stringLower = baseString.index(baseString.startIndex, offsetBy: charStart)
            let stringUpper = baseString.index(baseString.startIndex, offsetBy: charEnd)
            let nsRange = NSRange(stringLower..<stringUpper, in: baseString)
            guard nsRange.location + nsRange.length <= plainNS.length else { continue }

            if let intent = run.presentationIntent {
                for component in intent.components {
                    switch component.kind {
                    case .header(level: let level):
                        let size: CGFloat = level == 1 ? 20 : level == 2 ? 16 : 13
                        ns.addAttribute(.font, value: NSFont.boldSystemFont(ofSize: size), range: nsRange)
                    case .codeBlock:
                        ns.addAttribute(
                            .font,
                            value: NSFont.monospacedSystemFont(ofSize: 10, weight: .regular),
                            range: nsRange
                        )
                    default:
                        break
                    }
                }
            }
            if run.inlinePresentationIntent?.contains(.code) == true {
                ns.addAttribute(
                    .font,
                    value: NSFont.monospacedSystemFont(ofSize: 10, weight: .regular),
                    range: nsRange
                )
            }
            if run.inlinePresentationIntent?.contains(.stronglyEmphasized) == true {
                ns.addAttribute(.font, value: NSFont.boldSystemFont(ofSize: 11), range: nsRange)
            }
        }
    }

    // MARK: - Save panel

    private static func save(data: Data, defaultName: String) throws -> URL {
        let panel = NSSavePanel()
        panel.title = "Export to PDF"
        panel.allowedContentTypes = [UTType.pdf]
        panel.nameFieldStringValue = defaultName
        panel.canCreateDirectories = true
        panel.isExtensionHidden = false

        let response = panel.runModal()
        guard response == .OK, let url = panel.url else {
            throw ExportError.userCancelled
        }

        do {
            try data.write(to: url, options: .atomic)
            return url
        } catch {
            throw ExportError.write(error)
        }
    }

    // MARK: - Filenames

    private static func defaultFilename(for session: ChatSession) -> String {
        let stamp = filenameDate(session.updatedAt)
        return "\(safeFilename(session.displayTitle)) — \(stamp).pdf"
    }

    private static func defaultFilename(for note: Note) -> String {
        let stamp = filenameDate(note.updatedAt)
        return "\(safeFilename(note.title)) — \(stamp).pdf"
    }

    private static func filenameDate(_ date: Date) -> String {
        let formatter = DateFormatter()
        formatter.dateFormat = "yyyy-MM-dd"
        return formatter.string(from: date)
    }

    private static func safeFilename(_ raw: String) -> String {
        let stripped = raw
            .replacingOccurrences(of: "/", with: "-")
            .replacingOccurrences(of: ":", with: "-")
            .replacingOccurrences(of: "\\", with: "-")
            .trimmingCharacters(in: .whitespacesAndNewlines)
        let trimmed = stripped.isEmpty ? "Untitled" : stripped
        return String(trimmed.prefix(80))
    }
}
