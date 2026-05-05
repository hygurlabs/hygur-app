import Foundation

/// Represents a project that groups knowledge items together.
struct Project: Identifiable, Codable, Equatable, Sendable {
    let id: String
    let name: String
    let description: String?
    let tags: [String]
    let itemCount: Int
    let archived: Bool
    let createdAt: Date
    let updatedAt: Date

    enum CodingKeys: String, CodingKey {
        case id, name, description, tags, archived
        case itemCount = "item_count"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }

    init(
        id: String,
        name: String,
        description: String? = nil,
        tags: [String] = [],
        itemCount: Int = 0,
        archived: Bool = false,
        createdAt: Date = Date(),
        updatedAt: Date = Date()
    ) {
        self.id = id
        self.name = name
        self.description = description
        self.tags = tags
        self.itemCount = itemCount
        self.archived = archived
        self.createdAt = createdAt
        self.updatedAt = updatedAt
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)

        id = try container.decode(String.self, forKey: .id)
        name = try container.decode(String.self, forKey: .name)
        description = try container.decodeIfPresent(String.self, forKey: .description)
        tags = try container.decodeIfPresent([String].self, forKey: .tags) ?? []
        itemCount = try container.decode(Int.self, forKey: .itemCount)
        archived = try container.decode(Bool.self, forKey: .archived)

        // Parse ISO8601 dates
        let dateFormatter = ISO8601DateFormatter()
        dateFormatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]

        let createdAtString = try container.decode(String.self, forKey: .createdAt)
        if let date = dateFormatter.date(from: createdAtString) {
            createdAt = date
        } else {
            // Try without fractional seconds
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

/// Request body for creating a new project.
struct CreateProjectRequest: Codable, Sendable {
    let name: String
    let description: String?
    let tags: [String]?

    init(name: String, description: String? = nil, tags: [String]? = nil) {
        self.name = name
        self.description = description
        self.tags = tags
    }
}

/// Request body for updating an existing project.
struct UpdateProjectRequest: Codable, Sendable {
    let name: String?
    let description: String?
    let tags: [String]?
    let archived: Bool?

    init(name: String? = nil, description: String? = nil, tags: [String]? = nil, archived: Bool? = nil) {
        self.name = name
        self.description = description
        self.tags = tags
        self.archived = archived
    }
}

/// Represents a knowledge item within a project.
struct ProjectItem: Identifiable, Codable, Equatable, Hashable, Sendable {
    let id: String
    let title: String
    let sourceType: String
    let sourcePath: String?
    let createdAt: Date
    let updatedAt: Date

    enum CodingKeys: String, CodingKey {
        case id, title
        case sourceType = "source_type"
        case sourcePath = "source_path"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)

        id = try container.decode(String.self, forKey: .id)
        title = try container.decode(String.self, forKey: .title)
        sourceType = try container.decode(String.self, forKey: .sourceType)
        sourcePath = try container.decodeIfPresent(String.self, forKey: .sourcePath)

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

    var sourceTypeIcon: String {
        switch sourceType {
        case "note": return "note.text"
        case "email", "mail": return "envelope"
        case "markdown", "md": return "doc.text"
        case "pdf": return "doc.richtext"
        default: return "doc"
        }
    }
}

/// Response wrapper for project items endpoint.
struct ProjectItemsResponse: Codable, Sendable {
    let projectId: String
    let items: [ProjectItem]

    enum CodingKeys: String, CodingKey {
        case projectId = "project_id"
        case items
    }
}
