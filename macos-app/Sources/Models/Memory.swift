import Foundation

/// A durable memory persisted by the sidecar. Mirrors `handlers.StoreResponse`
/// — the same shape is returned by `/memory/list`, `/memory/sync`, and the
/// auto-extractor that fires after each chat turn.
struct MemoryItem: Identifiable, Codable, Hashable, Sendable {
    let memoryId: String
    let type: String       // "fact" | "preference" | "action"
    let content: String
    let createdAt: String  // RFC3339

    var id: String { memoryId }

    enum CodingKeys: String, CodingKey {
        case memoryId = "memory_id"
        case type
        case content
        case createdAt = "created_at"
    }
}

/// Response wrapper for `/memory/list`.
struct MemoryListResponse: Codable, Sendable {
    let memories: [MemoryItem]
    let total: Int
}

/// User-facing classification of a memory. Drives badge color and grouping
/// in `MemoriesView`. Falls back to `.fact` for unknown server values.
enum MemoryKind: String, Sendable {
    case fact
    case preference
    case action

    init(raw: String) {
        self = MemoryKind(rawValue: raw.lowercased()) ?? .fact
    }

    var label: String {
        switch self {
        case .fact:       return "Fait"
        case .preference: return "Préférence"
        case .action:     return "Action"
        }
    }
}
