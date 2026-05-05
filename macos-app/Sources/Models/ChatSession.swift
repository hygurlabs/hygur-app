import Foundation

/// Represents a chat session containing multiple messages.
struct ChatSession: Identifiable, Codable, Equatable {
    let id: UUID
    var title: String
    var messages: [Message]
    var projectId: String?
    var tagIds: [String]
    var isPinned: Bool
    let createdAt: Date
    var updatedAt: Date

    init(
        id: UUID = UUID(),
        title: String = "New Chat",
        messages: [Message] = [],
        projectId: String? = nil,
        tagIds: [String] = [],
        isPinned: Bool = false,
        createdAt: Date = Date(),
        updatedAt: Date = Date()
    ) {
        self.id = id
        self.title = title
        self.messages = messages
        self.projectId = projectId
        self.tagIds = tagIds
        self.isPinned = isPinned
        self.createdAt = createdAt
        self.updatedAt = updatedAt
    }

    /// Auto-generate title from first user message if not set
    var displayTitle: String {
        if title != "New Chat" && !title.isEmpty {
            return title
        }
        // Find first user message and use truncated content as title
        if let firstUserMessage = messages.first(where: { $0.role == .user }) {
            let content = firstUserMessage.content
            let truncated = String(content.prefix(50))
            return truncated.count < content.count ? "\(truncated)..." : truncated
        }
        return title
    }

    /// Preview of last message for sidebar display
    var lastMessagePreview: String? {
        guard let lastMessage = messages.last else { return nil }
        let content = lastMessage.content
        let truncated = String(content.prefix(80))
        return truncated.count < content.count ? "\(truncated)..." : truncated
    }

    /// Whether this session has any messages
    var hasMessages: Bool {
        !messages.isEmpty
    }

    /// Count of messages in the session
    var messageCount: Int {
        messages.count
    }
}

// MARK: - Message Codable Conformance

extension Message: Codable {
    enum CodingKeys: String, CodingKey {
        case id, role, content, timestamp, ragContext
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(UUID.self, forKey: .id)
        role = try container.decode(Role.self, forKey: .role)
        content = try container.decode(String.self, forKey: .content)
        timestamp = try container.decode(Date.self, forKey: .timestamp)
        ragContext = try container.decodeIfPresent(RAGContext.self, forKey: .ragContext)
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(id, forKey: .id)
        try container.encode(role, forKey: .role)
        try container.encode(content, forKey: .content)
        try container.encode(timestamp, forKey: .timestamp)
        try container.encodeIfPresent(ragContext, forKey: .ragContext)
    }
}

