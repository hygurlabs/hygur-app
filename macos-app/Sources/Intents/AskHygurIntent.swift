import AppIntents
import Foundation

/// "Ask Hygur" — answers a one-shot question via RAG against the user's
/// indexed knowledge base. Reuses the same `streamRAGChat` path the chat
/// UI uses (just aggregated to a single string for Siri / Shortcuts).
struct AskHygurIntent: AppIntent {
    static let title: LocalizedStringResource = "Ask Hygur"
    static let description = IntentDescription(
        "Ask a question and get an answer grounded in your Hygur knowledge base.",
        categoryName: "Hygur"
    )

    /// Read-only intent — no need to pop the app open. The user can launch
    /// Hygur manually from Spotlight if they want to keep digging.
    static let openAppWhenRun: Bool = false

    @Parameter(
        title: "Question",
        description: "What you want to ask Hygur about your knowledge base.",
        inputOptions: String.IntentInputOptions(
            keyboardType: .default,
            capitalizationType: .sentences,
            multiline: true
        )
    )
    var query: String

    static var parameterSummary: some ParameterSummary {
        Summary("Ask Hygur \(\.$query)")
    }

    func perform() async throws -> some IntentResult & ProvidesDialog & ReturnsValue<String> {
        let trimmed = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            throw HygurIntentError.missingInput("Please provide a question to ask Hygur.")
        }

        let service = HygurIntentSupport.service()
        let messages = [ChatMessage(role: "user", content: trimmed)]
        let stream = await service.streamRAGChat(messages: messages)

        let answer: String
        do {
            answer = try await HygurIntentSupport.aggregateRAGStream(stream)
        } catch let intentError as HygurIntentError {
            throw intentError
        } catch {
            throw HygurIntentError.operationFailed(error.localizedDescription)
        }

        return .result(value: answer, dialog: IntentDialog(stringLiteral: answer))
    }
}
