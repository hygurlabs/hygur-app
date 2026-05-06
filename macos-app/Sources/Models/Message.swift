import Foundation

struct GenerationStats: Equatable {
    let duration: TimeInterval
    let completionTokens: Int?
    let totalTokens: Int?

    var formattedDuration: String { "\(Int(duration.rounded()))s" }
    var formattedTokens: String? { completionTokens.map { "\($0) tokens" } }
}

struct Message: Identifiable, Equatable {
    let id: UUID
    let role: Role
    var content: String
    let timestamp: Date
    var ragContext: RAGContext?
    var generationStats: GenerationStats?
    var toolCalls: [ToolCall]?

    init(id: UUID = UUID(), role: Role, content: String, timestamp: Date = Date(), ragContext: RAGContext? = nil) {
        self.id = id
        self.role = role
        self.content = content
        self.timestamp = timestamp
        self.ragContext = ragContext
    }

    enum Role: String, Codable {
        case user
        case assistant
        case system
    }

    /// Whether this message has RAG context with sources
    var hasRAGContext: Bool {
        guard let context = ragContext else { return false }
        return !context.sources.isEmpty
    }

    /// Whether the model invoked any tools while producing this message.
    var hasToolCalls: Bool {
        guard let calls = toolCalls else { return false }
        return !calls.isEmpty
    }
}

/// One tool invocation observed during a streaming assistant turn. The
/// sidecar emits `tool_call` SSE events as it executes each call locally;
/// the chat client mirrors them into the message so the user sees what the
/// assistant did on their behalf (created a note, queried the calendar, …).
struct ToolCall: Identifiable, Equatable, Codable {
    /// Stable id assigned by the LLM (`tool_call_id` in OpenAI's wire format).
    /// Used to correlate the request and its result across rounds.
    let id: String
    /// Function name as registered in the sidecar's tool registry.
    let name: String
    /// Arguments object the model emitted, kept as a JSON string so the UI
    /// can render it without having to know each tool's schema.
    let arguments: String
    /// Tool output as a JSON string. Nil while the call is still running, or
    /// when the call returned an error (see `errorMessage`).
    var result: String?
    /// Set when the tool returned an error rather than a result.
    var errorMessage: String?
}
