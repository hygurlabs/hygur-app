import Foundation

/// A durable memory persisted by the sidecar. Mirrors `handlers.StoreResponse`
/// — the same shape is returned by `/memory/list`, `/memory/sync`, and the
/// auto-extractor that fires after each chat turn.
///
/// Phase 3.3 adds `source` and `acceptedAt`: extracted memories with a nil
/// `acceptedAt` are pending user review and surface in the "Pending review"
/// section of `MemoriesView`. Manual memories are auto-accepted by the
/// sidecar and have a non-nil `acceptedAt` from creation.
struct MemoryItem: Identifiable, Codable, Hashable, Sendable {
    let memoryId: String
    let type: String       // "fact" | "preference" | "action"
    let content: String
    let createdAt: String  // RFC3339
    let source: String?    // "manual" | "extracted" — nil = legacy/unknown (treated as manual)
    let acceptedAt: String? // RFC3339 or nil = pending review
    let sessionId: String?

    var id: String { memoryId }

    /// Convenience: pending = source extracted AND acceptedAt nil/empty.
    /// Mirrors the server-side query in `ListPendingMemories`.
    var isPending: Bool {
        let normalized = (source ?? "manual").lowercased()
        return normalized == "extracted" && (acceptedAt ?? "").isEmpty
    }

    /// Convenience: was this row auto-distilled rather than user-added?
    var isExtracted: Bool {
        (source ?? "manual").lowercased() == "extracted"
    }

    enum CodingKeys: String, CodingKey {
        case memoryId = "memory_id"
        case type
        case content
        case createdAt = "created_at"
        case source
        case acceptedAt = "accepted_at"
        case sessionId = "session_id"
    }
}

/// Response wrapper for `/memory/list` and `/memory/pending`.
struct MemoryListResponse: Codable, Sendable {
    let memories: [MemoryItem]
    let total: Int
}

/// Single user/assistant turn the macOS app forwards to the sidecar's
/// session extractor. Mirrors `handlers.ExtractMessagePayload`. We send
/// only the payload the LLM needs (role + content) — the chat session's
/// rich `Message` type holds RAG context and tool calls that the
/// extractor doesn't use.
struct MemoryExtractMessage: Codable, Sendable {
    let role: String
    let content: String
}

/// Body for `POST /memory/extract`. Mirrors `handlers.ExtractRequest`.
struct MemoryExtractRequest: Codable, Sendable {
    let sessionId: String
    let messages: [MemoryExtractMessage]

    enum CodingKeys: String, CodingKey {
        case sessionId = "session_id"
        case messages
    }
}

/// Response wrapper for `POST /memory/extract`. Mirrors
/// `handlers.ExtractResponse`. The macOS app uses `pending` to populate the
/// pending-review queue without having to round-trip to `/memory/pending`.
struct MemoryExtractResponse: Codable, Sendable {
    let extracted: Int
    let stored: Int
    let pending: [MemoryItem]?
}

/// Response wrapper for `DELETE /memory/extracted`. Mirrors
/// `handlers.ClearExtractedResponse`. Surfaces the count for UI feedback.
struct MemoryClearExtractedResponse: Codable, Sendable {
    let deleted: Int
}

/// Counts the sidecar reports for the Settings UI.
struct MemoryStatsResponse: Codable, Sendable {
    let manualCount: Int
    let extractedCount: Int
    let pendingCount: Int

    enum CodingKeys: String, CodingKey {
        case manualCount = "manual_count"
        case extractedCount = "extracted_count"
        case pendingCount = "pending_count"
    }
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
        case .fact:       return "Fact"
        case .preference: return "Preference"
        case .action:     return "Action"
        }
    }
}
