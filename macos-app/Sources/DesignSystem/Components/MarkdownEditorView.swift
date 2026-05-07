import SwiftUI
import AppKit

// MARK: - Public API

/// Native macOS markdown editor with live syntax highlighting and a Notion-style
/// formatting toolbar. Wraps `NSTextView` so we get proper undo, IME, find/replace,
/// drag-and-drop, and copy/paste behavior for free.
///
/// Use this in place of `TextEditor` whenever the content is markdown:
///
/// ```swift
/// MarkdownEditorView(text: $note.content)
///     .frame(minHeight: 240)
/// ```
///
/// Pass `showToolbar: false` for compact embeddings (e.g., the chat composer)
/// where a dedicated toolbar would be visual noise.
struct MarkdownEditorView: View {
    @Binding var text: String
    var prompt: String = ""
    var showToolbar: Bool = true
    var bordered: Bool = true
    var minHeight: CGFloat = 240
    var insets: NSSize = NSSize(width: 12, height: 12)
    var font: NSFont = .monospacedSystemFont(ofSize: 13, weight: .regular)
    var onSubmit: (() -> Void)?

    @StateObject private var commands = MarkdownEditorCommands()

    var body: some View {
        VStack(spacing: 0) {
            if showToolbar {
                MarkdownToolbar(commands: commands)
                Divider()
            }

            MarkdownTextView(
                text: $text,
                prompt: prompt,
                commands: commands,
                font: font,
                insets: insets,
                onSubmit: onSubmit
            )
            .frame(minHeight: minHeight)
        }
        .modifier(BorderedContainer(enabled: bordered))
    }
}

private struct BorderedContainer: ViewModifier {
    let enabled: Bool

    func body(content: Content) -> some View {
        if enabled {
            content
                .background(HygurColors.surface)
                .clipShape(RoundedRectangle(cornerRadius: HygurRadius.md))
                .overlay(
                    RoundedRectangle(cornerRadius: HygurRadius.md)
                        .strokeBorder(HygurColors.border, lineWidth: 1)
                )
        } else {
            content
        }
    }
}

// MARK: - Commands

/// Bridge between the SwiftUI toolbar and the underlying `NSTextView`.
/// The text view registers itself on appear; the toolbar dispatches edit
/// primitives (wrap selection, prefix line, insert template).
@MainActor
final class MarkdownEditorCommands: ObservableObject {
    fileprivate weak var textView: NSTextView?

    // MARK: Primitives

    func wrapSelection(prefix: String, suffix: String? = nil, placeholder: String = "") {
        guard let tv = textView, let storage = tv.textStorage else { return }
        let suffixStr = suffix ?? prefix
        let selected = tv.selectedRange()
        let nsString = storage.string as NSString

        if selected.length == 0 {
            let insert = "\(prefix)\(placeholder)\(suffixStr)"
            tv.insertText(insert, replacementRange: selected)
            let cursor = selected.location + (prefix as NSString).length
            let length = (placeholder as NSString).length
            tv.setSelectedRange(NSRange(location: cursor, length: length))
        } else {
            let selectedText = nsString.substring(with: selected)
            tv.insertText("\(prefix)\(selectedText)\(suffixStr)", replacementRange: selected)
            let cursor = selected.location + (prefix as NSString).length
            tv.setSelectedRange(NSRange(location: cursor, length: (selectedText as NSString).length))
        }
    }

    func prefixLine(prefix: String) {
        guard let tv = textView, let storage = tv.textStorage else { return }
        let nsString = storage.string as NSString
        let selected = tv.selectedRange()
        let lineRange = nsString.lineRange(for: selected)
        tv.insertText(prefix, replacementRange: NSRange(location: lineRange.location, length: 0))
        let cursor = selected.location + (prefix as NSString).length
        tv.setSelectedRange(NSRange(location: cursor, length: selected.length))
    }

    func insertTemplate(_ template: String, cursorOffset: Int? = nil) {
        guard let tv = textView else { return }
        let selected = tv.selectedRange()
        tv.insertText(template, replacementRange: selected)
        if let offset = cursorOffset {
            tv.setSelectedRange(NSRange(location: selected.location + offset, length: 0))
        }
    }

    // MARK: Convenience

    func bold() { wrapSelection(prefix: "**", placeholder: "bold") }
    func italic() { wrapSelection(prefix: "*", placeholder: "italic") }
    func strikethrough() { wrapSelection(prefix: "~~", placeholder: "text") }
    func inlineCode() { wrapSelection(prefix: "`", placeholder: "code") }
    func heading(_ level: Int) {
        prefixLine(prefix: String(repeating: "#", count: level) + " ")
    }
    func bulletList() { prefixLine(prefix: "- ") }
    func numberedList() { prefixLine(prefix: "1. ") }
    func quote() { prefixLine(prefix: "> ") }
    func link() { wrapSelection(prefix: "[", suffix: "](https://)", placeholder: "text") }

    func codeBlock() {
        insertTemplate("\n```\n\n```\n", cursorOffset: 5)
    }

    func table() {
        let template = """

        | Column 1 | Column 2 | Column 3 |
        | --- | --- | --- |
        | | | |
        | | | |

        """
        insertTemplate(template)
    }

    func horizontalRule() {
        insertTemplate("\n\n---\n\n")
    }
}

// MARK: - Toolbar

private struct MarkdownToolbar: View {
    @ObservedObject var commands: MarkdownEditorCommands

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 2) {
                button("bold", help: "Bold (⌘B)") { commands.bold() }
                button("italic", help: "Italic (⌘I)") { commands.italic() }
                button("strikethrough", help: "Strikethrough") { commands.strikethrough() }
                button("chevron.left.forwardslash.chevron.right", help: "Inline code") { commands.inlineCode() }

                divider

                Menu {
                    Button("Heading 1") { commands.heading(1) }
                    Button("Heading 2") { commands.heading(2) }
                    Button("Heading 3") { commands.heading(3) }
                } label: {
                    HStack(spacing: 2) {
                        Image(systemName: "textformat.size")
                        Image(systemName: "chevron.down").font(.system(size: 8))
                    }
                    .frame(height: 22)
                    .padding(.horizontal, 6)
                }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .fixedSize()
                .help("Heading")

                divider

                button("list.bullet", help: "Bullet list") { commands.bulletList() }
                button("list.number", help: "Numbered list") { commands.numberedList() }
                button("text.quote", help: "Quote") { commands.quote() }

                divider

                button("curlybraces", help: "Code block") { commands.codeBlock() }
                button("tablecells", help: "Table") { commands.table() }
                button("link", help: "Link") { commands.link() }
                button("minus", help: "Divider") { commands.horizontalRule() }

                Spacer(minLength: 0)
            }
            .padding(.horizontal, HygurSpacing.sm)
            .padding(.vertical, HygurSpacing.xs)
        }
        .font(.system(size: 13, weight: .medium))
        .foregroundStyle(HygurColors.textSecondary)
    }

    private func button(_ icon: String, help: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: icon)
                .frame(width: 26, height: 22)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(help)
    }

    private var divider: some View {
        Rectangle()
            .fill(HygurColors.divider)
            .frame(width: 1, height: 14)
            .padding(.horizontal, 4)
    }
}

// MARK: - NSTextView Bridge

private struct MarkdownTextView: NSViewRepresentable {
    @Binding var text: String
    let prompt: String
    let commands: MarkdownEditorCommands
    let font: NSFont
    let insets: NSSize
    let onSubmit: (() -> Void)?

    func makeNSView(context: Context) -> NSScrollView {
        let scroll = NSScrollView()
        scroll.hasVerticalScroller = true
        scroll.hasHorizontalScroller = false
        scroll.drawsBackground = false
        scroll.borderType = .noBorder
        scroll.autohidesScrollers = true

        let textView = MarkdownNSTextView()
        textView.delegate = context.coordinator
        textView.allowsUndo = true
        textView.isRichText = false
        textView.isEditable = true
        textView.isSelectable = true
        textView.smartInsertDeleteEnabled = false
        textView.isAutomaticQuoteSubstitutionEnabled = false
        textView.isAutomaticDashSubstitutionEnabled = false
        textView.isAutomaticTextReplacementEnabled = false
        textView.isAutomaticSpellingCorrectionEnabled = false
        textView.isAutomaticDataDetectionEnabled = false
        textView.isAutomaticLinkDetectionEnabled = false
        textView.usesFindBar = true
        textView.font = font
        textView.textColor = .labelColor
        textView.backgroundColor = .clear
        textView.drawsBackground = false
        textView.textContainerInset = insets
        textView.minSize = NSSize(width: 0, height: 0)
        textView.maxSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
        textView.isVerticallyResizable = true
        textView.isHorizontallyResizable = false
        textView.autoresizingMask = [.width]
        textView.textContainer?.containerSize = NSSize(
            width: 0,
            height: CGFloat.greatestFiniteMagnitude
        )
        textView.textContainer?.widthTracksTextView = true

        textView.string = text
        textView.commands = commands
        textView.placeholderText = prompt
        textView.onSubmit = onSubmit

        scroll.documentView = textView

        commands.textView = textView
        context.coordinator.applyHighlighting(textView.textStorage)

        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
        guard let textView = scroll.documentView as? MarkdownNSTextView else { return }

        if textView.string != text {
            let selected = textView.selectedRange()
            textView.string = text
            let safeLocation = min(selected.location, (text as NSString).length)
            textView.setSelectedRange(NSRange(location: safeLocation, length: 0))
            context.coordinator.applyHighlighting(textView.textStorage)
        }

        // Avoid spurious `needsDisplay = true` (placeholderText didSet) on
        // every parent re-render — set only when actually changed.
        if textView.placeholderText != prompt {
            textView.placeholderText = prompt
        }
        textView.onSubmit = onSubmit
        textView.commands = commands
        commands.textView = textView
    }

    /// Honor SwiftUI's proposed size verbatim when both axes are bounded.
    /// Without this, the wrapped `NSTextView`'s growth (it's
    /// `isVerticallyResizable = true`) leaks upward as an intrinsic height,
    /// causing the parent HStack/VStack to reflow violently on sibling
    /// state changes (the chat composer's mic gesture used to rebuild the
    /// layout when state mutated). The scroll view clips and scrolls
    /// internally — that's exactly what we want for the chat composer.
    ///
    /// For unbounded proposals (the note editor lives in a ScrollView and
    /// receives nil/.infinity), return nil and let the outer
    /// `.frame(minHeight:)` drive sizing.
    func sizeThatFits(_ proposal: ProposedViewSize, nsView: NSScrollView, context: Context) -> CGSize? {
        guard let width = proposal.width, width.isFinite,
              let height = proposal.height, height.isFinite else {
            return nil
        }
        return CGSize(width: width, height: height)
    }

    func makeCoordinator() -> Coordinator {
        Coordinator(self)
    }

    @MainActor
    final class Coordinator: NSObject, NSTextViewDelegate {
        var parent: MarkdownTextView

        init(_ parent: MarkdownTextView) {
            self.parent = parent
        }

        func textDidChange(_ notification: Notification) {
            guard let tv = notification.object as? NSTextView else { return }
            parent.text = tv.string
            if let storage = tv.textStorage {
                MarkdownSyntaxHighlighter.apply(to: storage, baseFont: parent.font)
            }
        }

        func applyHighlighting(_ storage: NSTextStorage?) {
            guard let storage else { return }
            MarkdownSyntaxHighlighter.apply(to: storage, baseFont: parent.font)
        }
    }
}

// MARK: - NSTextView Subclass

/// `NSTextView` subclass that intercepts ⌘B / ⌘I / ⌘K / Return for markdown
/// shortcuts and draws a placeholder when empty.
private final class MarkdownNSTextView: NSTextView {
    weak var commands: MarkdownEditorCommands?
    var onSubmit: (() -> Void)?
    var placeholderText: String = "" {
        didSet { needsDisplay = true }
    }

    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        if event.modifierFlags.contains(.command), let chars = event.charactersIgnoringModifiers?.lowercased() {
            switch chars {
            case "b":
                commands?.bold()
                return true
            case "i":
                commands?.italic()
                return true
            default:
                break
            }
        }
        return super.performKeyEquivalent(with: event)
    }

    override func insertNewline(_ sender: Any?) {
        // Shift+Return submits when an onSubmit handler is wired (chat composer).
        // Plain Return inserts a newline (note editor behavior).
        if let onSubmit, NSApp.currentEvent?.modifierFlags.contains(.shift) == false {
            // For chat: plain Return submits, Shift+Return inserts newline.
            // We flip the convention based on whether onSubmit is wired.
            onSubmit()
            return
        }
        super.insertNewline(sender)
    }

    override func draw(_ dirtyRect: NSRect) {
        super.draw(dirtyRect)
        guard string.isEmpty, !placeholderText.isEmpty else { return }
        let attrs: [NSAttributedString.Key: Any] = [
            .foregroundColor: NSColor.placeholderTextColor,
            .font: font ?? NSFont.systemFont(ofSize: 13)
        ]
        let inset = textContainerInset
        let origin = NSPoint(x: inset.width + 5, y: inset.height)
        (placeholderText as NSString).draw(at: origin, withAttributes: attrs)
    }

    override var acceptsFirstResponder: Bool { true }

    // Don't expose the text-content layout height as an intrinsic size to
    // SwiftUI. With `isVerticallyResizable = true`, AppKit will already
    // grow the view to fit the content; bubbling that as `intrinsicSize`
    // forces SwiftUI to re-resolve the parent layout on every keystroke
    // and breaks fixed-height containers like the chat composer.
    override var intrinsicContentSize: NSSize {
        NSSize(width: NSView.noIntrinsicMetric, height: NSView.noIntrinsicMetric)
    }
}

// MARK: - Syntax Highlighter

/// Pure-function highlighter that paints attribute spans on an `NSTextStorage`
/// based on regex matches. Rules are applied in order; later rules can override
/// earlier ones (e.g., code fence beats inline emphasis inside).
enum MarkdownSyntaxHighlighter {
    static func apply(to storage: NSTextStorage, baseFont: NSFont) {
        let str = storage.string
        let nsStr = str as NSString
        let full = NSRange(location: 0, length: nsStr.length)

        storage.beginEditing()

        // Reset to baseline first.
        storage.setAttributes([
            .font: baseFont,
            .foregroundColor: NSColor.labelColor
        ], range: full)

        let boldFont = NSFontManager.shared.convert(baseFont, toHaveTrait: .boldFontMask)
        let italicFont = NSFontManager.shared.convert(baseFont, toHaveTrait: .italicFontMask)
        let boldItalicFont = NSFontManager.shared.convert(boldFont, toHaveTrait: .italicFontMask)
        let monoFont = NSFont.monospacedSystemFont(ofSize: baseFont.pointSize, weight: .regular)

        let accent = NSColor.controlAccentColor
        let secondary = NSColor.secondaryLabelColor
        let codeFG = NSColor.systemPink
        let codeBG = NSColor(name: nil) { appearance in
            let isDark = appearance.bestMatch(from: [.darkAqua, .vibrantDark]) != nil
            return isDark
                ? NSColor(white: 0.18, alpha: 1.0)
                : NSColor(white: 0.94, alpha: 1.0)
        }

        // Heading: # to ###### at line start. Larger + bold + accent.
        regex("^(#{1,6})\\s+.*$", options: [.anchorsMatchLines]).matches(in: str, range: full)
            .forEach { match in
                let level = countLeadingHashes(in: nsStr, range: match.range)
                let scale: CGFloat = [1.45, 1.3, 1.18, 1.1, 1.05, 1.0][min(max(level - 1, 0), 5)]
                let headingFont = NSFontManager.shared.convert(
                    baseFont.withSize(baseFont.pointSize * scale),
                    toHaveTrait: .boldFontMask
                )
                storage.addAttributes([
                    .font: headingFont,
                    .foregroundColor: accent
                ], range: match.range)
            }

        // Code fences (multi-line). Apply early — inner emphasis should not
        // override the mono treatment.
        regex("```[\\s\\S]*?```", options: []).matches(in: str, range: full)
            .forEach { match in
                storage.addAttributes([
                    .font: monoFont,
                    .foregroundColor: NSColor.labelColor,
                    .backgroundColor: codeBG
                ], range: match.range)
            }

        // Inline code `...`
        regex("`[^`\\n]+`", options: []).matches(in: str, range: full)
            .forEach { match in
                storage.addAttributes([
                    .font: monoFont,
                    .foregroundColor: codeFG,
                    .backgroundColor: codeBG
                ], range: match.range)
            }

        // Bold-italic ***text***
        regex("\\*\\*\\*[^*\\n]+\\*\\*\\*", options: []).matches(in: str, range: full)
            .forEach { match in
                storage.addAttribute(.font, value: boldItalicFont, range: match.range)
            }

        // Bold **text**
        regex("\\*\\*[^*\\n]+\\*\\*", options: []).matches(in: str, range: full)
            .forEach { match in
                storage.addAttribute(.font, value: boldFont, range: match.range)
            }

        // Italic *text* — exclude double-star to avoid matching bold.
        regex("(?<![*])\\*(?!\\*)[^*\\n]+\\*(?!\\*)", options: []).matches(in: str, range: full)
            .forEach { match in
                storage.addAttribute(.font, value: italicFont, range: match.range)
            }

        // Strikethrough ~~text~~
        regex("~~[^~\\n]+~~", options: []).matches(in: str, range: full)
            .forEach { match in
                storage.addAttribute(.strikethroughStyle, value: NSUnderlineStyle.single.rawValue, range: match.range)
            }

        // Links [text](url) — color accent + underline the label part.
        regex("\\[[^\\]\\n]+\\]\\([^\\)\\n]+\\)", options: []).matches(in: str, range: full)
            .forEach { match in
                storage.addAttributes([
                    .foregroundColor: accent,
                    .underlineStyle: NSUnderlineStyle.single.rawValue
                ], range: match.range)
            }

        // Blockquote
        regex("^>\\s.*$", options: [.anchorsMatchLines]).matches(in: str, range: full)
            .forEach { match in
                storage.addAttributes([
                    .foregroundColor: secondary,
                    .font: italicFont
                ], range: match.range)
            }

        // List markers (only the "- " or "1. " bit)
        regex("^\\s*([-*+]|\\d+\\.)\\s", options: [.anchorsMatchLines]).matches(in: str, range: full)
            .forEach { match in
                storage.addAttributes([
                    .foregroundColor: accent,
                    .font: boldFont
                ], range: match.range)
            }

        // Horizontal rule --- / *** / ___ on its own line
        regex("^\\s*([-*_])\\s*\\1\\s*\\1[\\-*_\\s]*$", options: [.anchorsMatchLines])
            .matches(in: str, range: full)
            .forEach { match in
                storage.addAttribute(.foregroundColor, value: secondary, range: match.range)
            }

        // Table delimiter row | --- | --- |
        regex("^\\s*\\|?(\\s*:?-+:?\\s*\\|)+\\s*:?-+:?\\s*\\|?\\s*$", options: [.anchorsMatchLines])
            .matches(in: str, range: full)
            .forEach { match in
                storage.addAttributes([
                    .foregroundColor: secondary,
                    .font: monoFont
                ], range: match.range)
            }

        storage.endEditing()
    }

    private static func regex(_ pattern: String, options: NSRegularExpression.Options) -> NSRegularExpression {
        // swiftlint:disable:next force_try
        try! NSRegularExpression(pattern: pattern, options: options)
    }

    private static func countLeadingHashes(in str: NSString, range: NSRange) -> Int {
        var count = 0
        let end = min(range.location + 6, range.location + range.length)
        for i in range.location..<end {
            if str.character(at: i) == 0x23 { count += 1 } else { break }
        }
        return count
    }
}

// MARK: - Preview

#Preview {
    @Previewable @State var text = """
    # Heading 1

    Some **bold** and *italic* and `inline code` plus a [link](https://example.com).

    ## Heading 2

    > A quote line that should look italic and dim.

    - Bullet item one
    - Bullet item two
    1. Numbered item

    ```swift
    func hello() {
        print("Hi from a code block")
    }
    ```

    | Column A | Column B |
    | --- | --- |
    | a | b |
    """

    MarkdownEditorView(text: $text)
        .frame(width: 600, height: 500)
        .padding()
}
