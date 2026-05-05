import SwiftUI
import MarkdownUI

/// Renders markdown content with citation badge support for assistant messages.
/// Combines MarkdownUI rendering with the existing citation pattern parsing.
struct MarkdownMessageView: View {
    let content: String
    let sources: [RAGSource]
    var onCitationTap: ((Int) -> Void)?

    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        Markdown(content)
            .markdownTheme(chatTheme)
            .markdownCodeSyntaxHighlighter(CodeTheme())
            .textSelection(.enabled)
    }

    // MARK: - Theme

    private var chatTheme: Theme {
        Theme()
            .text {
                ForegroundColor(.primary)
                FontSize(.em(1))
            }
            .strong {
                FontWeight(.semibold)
            }
            .emphasis {
                FontStyle(.italic)
            }
            .link {
                ForegroundColor(.accentColor)
                UnderlineStyle(.single)
            }
            .code {
                FontFamilyVariant(.monospaced)
                FontSize(.em(0.9))
                BackgroundColor(codeBackgroundColor)
                ForegroundColor(codeTextColor)
            }
            .codeBlock { configuration in
                codeBlockView(configuration)
            }
            .blockquote { configuration in
                HStack(spacing: 0) {
                    Rectangle()
                        .fill(Color.accentColor.opacity(0.6))
                        .frame(width: 3)

                    configuration.label
                        .markdownTextStyle {
                            ForegroundColor(.secondary)
                            FontStyle(.italic)
                        }
                        .padding(EdgeInsets(top: 0, leading: 12, bottom: 0, trailing: 0))
                }
            }
            .heading1 { configuration in
                configuration.label
                    .markdownMargin(top: .em(1), bottom: .em(0.5))
                    .markdownTextStyle {
                        FontWeight(.bold)
                        FontSize(.em(1.5))
                    }
            }
            .heading2 { configuration in
                configuration.label
                    .markdownMargin(top: .em(0.8), bottom: .em(0.4))
                    .markdownTextStyle {
                        FontWeight(.bold)
                        FontSize(.em(1.3))
                    }
            }
            .heading3 { configuration in
                configuration.label
                    .markdownMargin(top: .em(0.6), bottom: .em(0.3))
                    .markdownTextStyle {
                        FontWeight(.semibold)
                        FontSize(.em(1.15))
                    }
            }
            .paragraph { configuration in
                configuration.label
                    .markdownMargin(top: .zero, bottom: .em(0.5))
            }
    }

    // MARK: - Code Block View

    private func codeBlockView(_ configuration: CodeBlockConfiguration) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            // Language label (if present)
            if let language = configuration.language, !language.isEmpty {
                Text(language.uppercased())
                    .font(HygurTypography.captionMono)
                    .fontWeight(.medium)
                    .foregroundStyle(HygurColors.textSecondary)
                    .padding(.horizontal, HygurSpacing.md)
                    .padding(.top, HygurSpacing.sm)
                    .padding(.bottom, HygurSpacing.xs)
            }

            ScrollView(.horizontal, showsIndicators: true) {
                configuration.label
                    .markdownTextStyle {
                        FontFamilyVariant(.monospaced)
                        FontSize(.em(0.85))
                        ForegroundColor(codeTextColor)
                    }
                    .padding(.horizontal, HygurSpacing.md)
                    .padding(.vertical, configuration.language != nil ? HygurSpacing.sm : HygurSpacing.md)
            }
        }
        .background(codeBlockBackgroundColor)
        .clipShape(RoundedRectangle(cornerRadius: HygurRadius.md))
        .markdownMargin(top: .em(0.5), bottom: .em(0.5))
    }


    // MARK: - Colors

    private var codeBackgroundColor: Color {
        colorScheme == .dark
            ? Color(white: 0.15)
            : Color(white: 0.95)
    }

    private var codeBlockBackgroundColor: Color {
        colorScheme == .dark
            ? Color(white: 0.12)
            : Color(white: 0.96)
    }

    private var codeTextColor: Color {
        colorScheme == .dark
            ? Color(red: 0.9, green: 0.9, blue: 0.85)
            : Color(red: 0.2, green: 0.2, blue: 0.25)
    }

}

// MARK: - Code Syntax Highlighter

/// Basic code syntax highlighting theme
struct CodeTheme: CodeSyntaxHighlighter {
    func highlightCode(_ code: String, language: String?) -> Text {
        // For now, return plain monospace text
        // MarkdownUI's built-in syntax highlighting can be enabled with Splash or other highlighters
        Text(code)
    }
}


// MARK: - Preview

#Preview("Markdown Message") {
    let sources = [
        RAGSource(
            contentId: "doc-1",
            sourceType: "document",
            title: "Requirements Doc",
            excerpt: "The system requirements...",
            score: 0.9,
            mailFrom: nil,
            mailDate: nil,
            mailSubject: nil
        ),
        RAGSource(
            contentId: "mail-1",
            sourceType: "email",
            title: "Project Email",
            excerpt: "As discussed in our meeting...",
            score: 0.8,
            mailFrom: "test@example.com",
            mailDate: "2024-01-15",
            mailSubject: "Re: Project Update"
        )
    ]

    let sampleMarkdown = """
    # Project Overview

    Based on the requirements [Document 1], here are the key points:

    ## Features
    - **Real-time sync** with cloud storage
    - *Offline mode* support
    - Cross-platform compatibility

    The email thread [Email 2] confirms the timeline.

    ### Code Example
    ```swift
    func fetchData() async throws -> [Item] {
        let response = try await api.request(.items)
        return response.items
    }
    ```

    > Note: This is a blockquote with important information.

    For more details, see the [documentation](https://example.com).

    Inline `code` works too.
    """

    return ScrollView {
        MarkdownMessageView(
            content: sampleMarkdown,
            sources: sources
        ) { index in
            print("Tapped citation \(index)")
        }
        .padding()
    }
    .frame(width: 500, height: 600)
}

#Preview("Simple Text") {
    MarkdownMessageView(
        content: "This is a simple message without any markdown.",
        sources: []
    )
    .padding()
}

#Preview("Dark Mode") {
    let markdown = """
    ## Dark Mode Test

    Here is some **bold** and *italic* text.

    ```python
    def hello():
        print("Hello, World!")
    ```
    """

    return MarkdownMessageView(
        content: markdown,
        sources: []
    )
    .padding()
    .preferredColorScheme(.dark)
}
