import Foundation

/// Represents a note in the knowledge base.
struct Note: Identifiable, Codable, Equatable, Sendable {
    let id: String
    let title: String
    let content: String
    let projectId: String?
    let tags: [Tag]
    let createdAt: Date
    let updatedAt: Date

    enum CodingKeys: String, CodingKey {
        case id, title, content, tags
        case projectId = "project_id"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }

    init(
        id: String,
        title: String,
        content: String,
        projectId: String? = nil,
        tags: [Tag] = [],
        createdAt: Date = Date(),
        updatedAt: Date = Date()
    ) {
        self.id = id
        self.title = title
        self.content = content
        self.projectId = projectId
        self.tags = tags
        self.createdAt = createdAt
        self.updatedAt = updatedAt
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)

        id = try container.decode(String.self, forKey: .id)
        title = try container.decode(String.self, forKey: .title)
        content = try container.decode(String.self, forKey: .content)
        projectId = try container.decodeIfPresent(String.self, forKey: .projectId)
        tags = try container.decodeIfPresent([Tag].self, forKey: .tags) ?? []

        // Parse ISO8601 dates
        let dateFormatter = ISO8601DateFormatter()
        dateFormatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]

        let createdAtString = try container.decode(String.self, forKey: .createdAt)
        if let date = dateFormatter.date(from: createdAtString) {
            createdAt = date
        } else {
            dateFormatter.formatOptions = [.withInternetDateTime]
            createdAt = dateFormatter.date(from: createdAtString) ?? Date()
        }

        let updatedAtString = try container.decode(String.self, forKey: .updatedAt)
        if let date = dateFormatter.date(from: updatedAtString) {
            updatedAt = date
        } else {
            dateFormatter.formatOptions = [.withInternetDateTime]
            updatedAt = dateFormatter.date(from: updatedAtString) ?? Date()
        }
    }
}

// MARK: - Request/Response Types

/// Request body for creating a new note.
struct CreateNoteRequest: Codable, Sendable {
    let title: String
    let content: String
    let projectId: String?
    let tagIds: [String]?

    enum CodingKeys: String, CodingKey {
        case title, content
        case projectId = "project_id"
        case tagIds = "tag_ids"
    }

    init(title: String, content: String, projectId: String? = nil, tagIds: [String]? = nil) {
        self.title = title
        self.content = content
        self.projectId = projectId
        self.tagIds = tagIds
    }
}

/// Request body for updating an existing note.
struct UpdateNoteRequest: Codable, Sendable {
    let title: String?
    let content: String?
    let projectId: String?
    let tagIds: [String]?

    enum CodingKeys: String, CodingKey {
        case title, content
        case projectId = "project_id"
        case tagIds = "tag_ids"
    }

    init(title: String? = nil, content: String? = nil, projectId: String? = nil, tagIds: [String]? = nil) {
        self.title = title
        self.content = content
        self.projectId = projectId
        self.tagIds = tagIds
    }
}

/// Response wrapper for note list endpoint.
struct NoteListResponse: Codable, Sendable {
    let notes: [Note]
}
