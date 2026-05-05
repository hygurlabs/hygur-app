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
}
