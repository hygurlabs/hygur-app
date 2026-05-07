import Foundation

/// Errors surfaced by the App Intents layer. Conforms to `LocalizedError`
/// so Shortcuts and Siri can show the message verbatim.
enum HygurIntentError: LocalizedError {
    case sidecarUnavailable
    case missingInput(String)
    case operationFailed(String)

    var errorDescription: String? {
        switch self {
        case .sidecarUnavailable:
            return "Hygur isn't reachable. Make sure the app is running."
        case .missingInput(let detail):
            return detail
        case .operationFailed(let detail):
            return detail
        }
    }
}

/// Shared helpers for App Intents — kept tiny on purpose. Each intent
/// dispatches via the same `SidecarService` instance the rest of the app
/// uses (via `SidecarService.fromSettings()`), so no duplicate auth/HTTP
/// plumbing lives here.
enum HygurIntentSupport {
    /// Builds a `SidecarService` configured against the user's saved
    /// sidecar URL. Mirrors what `ContentView` and the view models do.
    static func service() -> SidecarService {
        SidecarService.fromSettings()
    }

    /// Aggregate a `streamRAGChat` AsyncSequence into a single string. We
    /// surface RAG sources as a tiny "Based on:" footer so the Siri
    /// response is self-contained without dragging the chat UI in.
    static func aggregateRAGStream(
        _ stream: AsyncThrowingStream<ChatStreamEvent, Error>
    ) async throws -> String {
        var content = ""
        var sources: [RAGSource] = []
        var streamError: String?

        for try await event in stream {
            switch event {
            case .ragContext(let context):
                sources = context.sources
            case .delta(let delta):
                content += delta
            case .toolCall:
                // Intents surface a single text answer to Siri/Shortcuts —
                // tool invocations belong to the in-app chat UI and add
                // nothing useful to the spoken/typed response.
                continue
            case .done:
                continue
            case .error(let message):
                streamError = message
            }
        }

        if let message = streamError, content.isEmpty {
            throw HygurIntentError.operationFailed(message)
        }

        let trimmed = content.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.isEmpty {
            return "Hygur didn't have an answer for that."
        }

        if sources.isEmpty {
            return trimmed
        }

        let topSources = sources.prefix(3).map { source -> String in
            let label = source.title.isEmpty ? source.sourceLabel : source.title
            return "• \(label)"
        }.joined(separator: "\n")

        return trimmed + "\n\nBased on:\n" + topSources
    }
}
