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
    var attachments: [Attachment]?

    init(id: UUID = UUID(), role: Role, content: String, timestamp: Date = Date(), ragContext: RAGContext? = nil, attachments: [Attachment]? = nil) {
        self.id = id
        self.role = role
        self.content = content
        self.timestamp = timestamp
        self.ragContext = ragContext
        self.attachments = attachments
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

    /// Whether this message carries any non-text payload.
    var hasAttachments: Bool {
        guard let list = attachments else { return false }
        return !list.isEmpty
    }
}

/// A non-text payload attached to a message. Mirrors the Hygur API
/// `attachments[]` shape consumed by the sidecar (see internal/llm.Attachment
/// in Go); the sidecar translates these into the runtime-specific multimodal
/// content blocks (OpenAI today; vLLM/NIM may differ).
///
/// Document attachments reference an already-ingested item by its
/// `contentId` and are resolved server-side to inline text — the LLM never
/// sees a `document` block.
enum Attachment: Equatable, Sendable {
    case image(data: Data, mimeType: String)
    case audio(data: Data, format: String, duration: TimeInterval?)
    case document(contentId: String, title: String?)
}

extension Attachment: Codable {
    private enum CodingKeys: String, CodingKey {
        case type
        case mimeType = "mime_type"
        case data
        case format
        case duration
        case contentId = "content_id"
        case title
    }

    private enum Kind: String, Codable {
        case image, audio, document
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        let kind = try container.decode(Kind.self, forKey: .type)
        switch kind {
        case .image:
            let data = try container.decode(Data.self, forKey: .data)
            let mime = try container.decode(String.self, forKey: .mimeType)
            self = .image(data: data, mimeType: mime)
        case .audio:
            let data = try container.decode(Data.self, forKey: .data)
            let format = try container.decode(String.self, forKey: .format)
            let duration = try container.decodeIfPresent(TimeInterval.self, forKey: .duration)
            self = .audio(data: data, format: format, duration: duration)
        case .document:
            let cid = try container.decode(String.self, forKey: .contentId)
            let title = try container.decodeIfPresent(String.self, forKey: .title)
            self = .document(contentId: cid, title: title)
        }
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        switch self {
        case .image(let data, let mime):
            try c.encode(Kind.image, forKey: .type)
            try c.encode(data, forKey: .data)
            try c.encode(mime, forKey: .mimeType)
        case .audio(let data, let format, let duration):
            try c.encode(Kind.audio, forKey: .type)
            try c.encode(data, forKey: .data)
            try c.encode(format, forKey: .format)
            try c.encodeIfPresent(duration, forKey: .duration)
        case .document(let cid, let title):
            try c.encode(Kind.document, forKey: .type)
            try c.encode(cid, forKey: .contentId)
            try c.encodeIfPresent(title, forKey: .title)
        }
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
