import AppIntents
import Foundation

/// "Create note in Hygur" — creates a note via the same `POST /notes`
/// endpoint the in-app notes UI and the chat tool both use. Embeddings are
/// generated server-side as part of `CreateNoteTool`, so the new note is
/// immediately searchable.
struct CreateNoteInHygurIntent: AppIntent {
    static let title: LocalizedStringResource = "Create note in Hygur"
    static let description = IntentDescription(
        "Create a new note in your Hygur knowledge base.",
        categoryName: "Hygur"
    )

    /// Background-only — the action is invisible-success. The user can
    /// open Hygur from Spotlight to inspect the new note.
    static let openAppWhenRun: Bool = false

    @Parameter(
        title: "Title",
        description: "Title of the note.",
        inputOptions: String.IntentInputOptions(
            keyboardType: .default,
            capitalizationType: .sentences
        )
    )
    var title: String

    @Parameter(
        title: "Body",
        description: "The note's content.",
        inputOptions: String.IntentInputOptions(
            keyboardType: .default,
            capitalizationType: .sentences,
            multiline: true
        )
    )
    var body: String

    static var parameterSummary: some ParameterSummary {
        Summary("Create note \(\.$title) in Hygur") {
            \.$body
        }
    }

    func perform() async throws -> some IntentResult & ProvidesDialog & ReturnsValue<String> {
        let trimmedTitle = title.trimmingCharacters(in: .whitespacesAndNewlines)
        let trimmedBody = body.trimmingCharacters(in: .whitespacesAndNewlines)

        guard !trimmedTitle.isEmpty else {
            throw HygurIntentError.missingInput("A title is required to create a note.")
        }
        guard !trimmedBody.isEmpty else {
            throw HygurIntentError.missingInput("The note body can't be empty.")
        }

        let service = HygurIntentSupport.service()
        do {
            let note = try await service.createNote(title: trimmedTitle, content: trimmedBody)
            let confirmation = "Created note \"\(note.title)\" in Hygur (id: \(note.id))."
            return .result(value: confirmation, dialog: IntentDialog(stringLiteral: confirmation))
        } catch {
            throw HygurIntentError.operationFailed(error.localizedDescription)
        }
    }
}
