import Foundation

// MARK: - Knowledge Item Response (from API)

struct KnowledgeItemResponse: Identifiable, Codable, Sendable {
    let contentId: String
    let sourceType: String
    let sourcePath: String?
    let title: String
    let normalizedText: String?
    let chunkCount: Int
    let tags: [TagSummary]
    let projectId: String?
    let createdAt: String
    let updatedAt: String
    let date: String?

    var id: String { contentId }

    var documentDate: Date {
        if let date = date, !date.isEmpty {
            let formatter = ISO8601DateFormatter()
            formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            return formatter.date(from: date) ?? Date()
        }
        return createdAtDate
    }

    var createdAtDate: Date {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter.date(from: createdAt) ?? Date()
    }

    var updatedAtDate: Date {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter.date(from: updatedAt) ?? Date()
    }

    enum CodingKeys: String, CodingKey {
        case contentId = "content_id"
        case sourceType = "source_type"
        case sourcePath = "source_path"
        case title
        case normalizedText = "normalized_text"
        case chunkCount = "chunk_count"
        case tags
        case projectId = "project_id"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
        case date
    }
}

struct TagSummary: Codable, Sendable {
    let id: String
    let name: String
    let color: String
}

// MARK: - Knowledge Item (legacy)

struct KnowledgeItem: Identifiable, Codable, Equatable, Sendable {
    let contentId: String
    let sourceType: String
    let sourcePath: String?
    let title: String
    let chunkCount: Int
    let tags: [Tag]
    let projectId: String?
    let createdAt: Date
    let updatedAt: Date

    var id: String { contentId }

    enum CodingKeys: String, CodingKey {
        case contentId = "content_id"
        case sourceType = "source_type"
        case sourcePath = "source_path"
        case title
        case chunkCount = "chunk_count"
        case tags
        case projectId = "project_id"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }

    init(
        contentId: String,
        sourceType: String,
        sourcePath: String?,
        title: String,
        chunkCount: Int,
        tags: [Tag] = [],
        projectId: String? = nil,
        createdAt: Date,
        updatedAt: Date
    ) {
        self.contentId = contentId
        self.sourceType = sourceType
        self.sourcePath = sourcePath
        self.title = title
        self.chunkCount = chunkCount
        self.tags = tags
        self.projectId = projectId
        self.createdAt = createdAt
        self.updatedAt = updatedAt
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)

        contentId = try container.decode(String.self, forKey: .contentId)
        sourceType = try container.decode(String.self, forKey: .sourceType)
        sourcePath = try container.decodeIfPresent(String.self, forKey: .sourcePath)
        title = try container.decode(String.self, forKey: .title)
        chunkCount = try container.decode(Int.self, forKey: .chunkCount)
        tags = try container.decodeIfPresent([Tag].self, forKey: .tags) ?? []
        projectId = try container.decodeIfPresent(String.self, forKey: .projectId)

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

// MARK: - Ingest Request/Response

struct IngestRequest: Codable, Sendable {
    let path: String
    let projectId: String?
    let tags: [String]?

    enum CodingKeys: String, CodingKey {
        case path
        case projectId = "project_id"
        case tags
    }
}

struct IngestResponse: Codable, Sendable {
    let contentId: String
    let status: IngestStatus
    let chunkCount: Int
    let title: String

    enum CodingKeys: String, CodingKey {
        case contentId = "content_id"
        case status
        case chunkCount = "chunk_count"
        case title
    }
}

enum IngestStatus: String, Codable, Sendable {
    case indexed
    case duplicate
    case nearDuplicate = "near_duplicate"
}

// MARK: - Folder Ingest Request/Response

struct IngestFolderRequest: Codable, Sendable {
    let path: String
    let projectId: String?
    let tags: [String]?
    let maxDepth: Int?
    let extensions: [String]?
    let ignorePatterns: [String]?

    enum CodingKeys: String, CodingKey {
        case path
        case projectId = "project_id"
        case tags
        case maxDepth = "max_depth"
        case extensions
        case ignorePatterns = "ignore_patterns"
    }
}

struct IngestFolderResponse: Codable, Sendable {
    let processed: Int
    let skipped: Int
    let failed: Int
    let totalChunks: Int
    let results: [IngestFolderResult]
    let errors: [IngestFolderError]

    enum CodingKeys: String, CodingKey {
        case processed, skipped, failed
        case totalChunks = "total_chunks"
        case results, errors
    }
}

struct IngestFolderResult: Codable, Sendable {
    let path: String
    let contentId: String
    let status: IngestStatus
    let chunkCount: Int

    enum CodingKeys: String, CodingKey {
        case path
        case contentId = "content_id"
        case status
        case chunkCount = "chunk_count"
    }
}

struct IngestFolderError: Codable, Sendable {
    let path: String
    let message: String
}

// MARK: - Knowledge List Response

struct KnowledgeListResponse: Codable, Sendable {
    let items: [KnowledgeItemResponse]
    let total: Int
    let limit: Int
    let offset: Int
}

// MARK: - Search Request/Response

struct SearchRequest: Codable, Sendable {
    let query: String
    let projectId: String?
    let dateFrom: String?
    let dateTo: String?
    let topK: Int

    enum CodingKeys: String, CodingKey {
        case query
        case projectId = "project_id"
        case dateFrom = "date_from"
        case dateTo = "date_to"
        case topK = "top_k"
    }
}

struct SearchResponse: Codable, Sendable {
    let results: [SearchResult]
    let total: Int
}

struct SearchResult: Identifiable, Codable, Sendable {
    let chunkId: String
    let contentId: String
    let score: Double
    let excerpt: String
    let title: String
    let source: String  // "fts", "vector", "both"
    let sourceType: String  // "knowledge", "mail"
    let date: Date?  // Date of the document/email

    var id: String { chunkId }

    enum CodingKeys: String, CodingKey {
        case chunkId = "chunk_id"
        case contentId = "content_id"
        case score, excerpt, title, source
        case sourceType = "source_type"
        case date
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)

        chunkId = try container.decode(String.self, forKey: .chunkId)
        contentId = try container.decode(String.self, forKey: .contentId)
        score = try container.decode(Double.self, forKey: .score)
        excerpt = try container.decodeIfPresent(String.self, forKey: .excerpt) ?? ""
        title = try container.decodeIfPresent(String.self, forKey: .title) ?? "Untitled"
        source = try container.decodeIfPresent(String.self, forKey: .source) ?? "fts"
        sourceType = try container.decodeIfPresent(String.self, forKey: .sourceType) ?? "knowledge"

        // Parse date from ISO8601 string
        if let dateString = try container.decodeIfPresent(String.self, forKey: .date) {
            let formatter = ISO8601DateFormatter()
            formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            date = formatter.date(from: dateString)
        } else {
            date = nil
        }
    }
}
