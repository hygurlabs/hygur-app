import AppIntents
import Foundation

/// "Save to Hygur" — accepts either free-form text or a URL and ingests it
/// into the knowledge base. There is no dedicated "ingest URL" endpoint
/// today, so both paths fall through to the existing notes endpoint
/// (`POST /notes`), which already creates a searchable knowledge item with
/// chunks + embeddings via `CreateNoteTool`.
struct SaveToHygurIntent: AppIntent {
    static let title: LocalizedStringResource = "Save to Hygur"
    static let description = IntentDescription(
        "Save text or a URL to your Hygur knowledge base.",
        categoryName: "Hygur"
    )

    /// Background-only — the UI doesn't need to come forward.
    static let openAppWhenRun: Bool = false

    @Parameter(
        title: "Text",
        description: "Free-form text to save. Leave empty if saving a URL.",
        default: nil,
        inputOptions: String.IntentInputOptions(
            keyboardType: .default,
            capitalizationType: .sentences,
            multiline: true
        )
    )
    var text: String?

    @Parameter(
        title: "URL",
        description: "URL to save. Leave empty if saving plain text."
    )
    var url: URL?

    static var parameterSummary: some ParameterSummary {
        Summary("Save \(\.$text) or \(\.$url) to Hygur")
    }

    func perform() async throws -> some IntentResult & ProvidesDialog & ReturnsValue<String> {
        let trimmedText = text?.trimmingCharacters(in: .whitespacesAndNewlines)
        let hasText = (trimmedText?.isEmpty == false)
        let hasURL = (url != nil)

        guard hasText || hasURL else {
            throw HygurIntentError.missingInput("Provide either text or a URL to save.")
        }

        let title: String
        let content: String

        if let urlValue = url, !hasText {
            title = "Saved URL — \(urlValue.host() ?? urlValue.absoluteString)"
            content = urlValue.absoluteString
        } else if hasURL, let urlValue = url, let body = trimmedText {
            // Both provided — keep both, the body wins as the title source.
            title = firstLine(of: body) ?? "Saved snippet"
            content = "\(body)\n\nSource: \(urlValue.absoluteString)"
        } else if let body = trimmedText {
            title = firstLine(of: body) ?? "Saved snippet"
            content = body
        } else {
            throw HygurIntentError.missingInput("Nothing to save.")
        }

        let service = HygurIntentSupport.service()
        do {
            let note = try await service.createNote(title: title, content: content)
            let confirmation: String
            if hasURL && !hasText {
                confirmation = "Saved to Hygur — URL indexed as \"\(note.title)\"."
            } else {
                confirmation = "Saved to Hygur — \"\(note.title)\" indexed."
            }
            return .result(value: confirmation, dialog: IntentDialog(stringLiteral: confirmation))
        } catch {
            throw HygurIntentError.operationFailed(error.localizedDescription)
        }
    }

    /// Pulls the first non-empty line of a string as a candidate title.
    /// Caps at 80 chars so a giant first paragraph doesn't blow up the
    /// notes list rendering.
    private func firstLine(of text: String) -> String? {
        for raw in text.split(whereSeparator: \.isNewline) {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if !line.isEmpty {
                return String(line.prefix(80))
            }
        }
        return nil
    }
}
