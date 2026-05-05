import Foundation

/// One row in a `TimelineChapter`. Mirrors `handlers.TimelineEventDTO` from
/// the sidecar.
struct TimelineEvent: Identifiable, Codable, Sendable, Hashable {
    let date: String
    let contentId: String
    let sourceType: String?
    let title: String?
    let snippet: String?
    let context: String?

    /// Composite identifier — content_id alone is not unique (a single doc
    /// can spawn multiple events through `extracted_event_dates`).
    var id: String { "\(contentId)@\(date)" }

    enum CodingKeys: String, CodingKey {
        case date
        case contentId = "content_id"
        case sourceType = "source_type"
        case title, snippet, context
    }
}

/// A clustered group of events sharing entities or topic within a time
/// bucket. Mirrors `handlers.TimelineChapterDTO`.
struct TimelineChapter: Identifiable, Codable, Sendable, Hashable {
    let id: String
    let title: String
    let timeStart: String
    let timeEnd: String
    let dominantEntities: [String]
    let eventCount: Int
    let events: [TimelineEvent]

    enum CodingKeys: String, CodingKey {
        case id, title
        case timeStart = "time_start"
        case timeEnd = "time_end"
        case dominantEntities = "dominant_entities"
        case eventCount = "event_count"
        case events
    }

    var parsedStart: Date { Self.parseISO(timeStart) ?? .distantPast }
    var parsedEnd:   Date { Self.parseISO(timeEnd)   ?? parsedStart  }

    /// Parses an ISO8601 datetime tolerating both fractional-second and
    /// plain RFC3339 formats. Go's `time.RFC3339` omits the fractional part
    /// when nanoseconds are zero, so we must try both.
    private static func parseISO(_ s: String) -> Date? {
        let withFractions = ISO8601DateFormatter()
        withFractions.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let d = withFractions.date(from: s) { return d }
        let plain = ISO8601DateFormatter()
        plain.formatOptions = [.withInternetDateTime]
        return plain.date(from: s)
    }
}

/// Response wrapper for POST `/timeline/query`. Mirrors
/// `handlers.TimelineResponseDTO`.
struct TimelineResponse: Codable, Sendable {
    let chapters: [TimelineChapter]
    let query: String
    let totalEvents: Int

    enum CodingKeys: String, CodingKey {
        case chapters, query
        case totalEvents = "total_events"
    }
}

/// Request body for POST `/timeline/query`.
struct TimelineQueryRequest: Codable, Sendable {
    let query: String
    let focusScope: TimelineFocusScope?
    let rangeDays: Int?
    let topDocs: Int?

    enum CodingKeys: String, CodingKey {
        case query
        case focusScope = "focus_scope"
        case rangeDays = "range_days"
        case topDocs = "top_docs"
    }
}

/// Focus scope payload — same shape as `retrieval.FocusScope` on the wire.
struct TimelineFocusScope: Codable, Sendable {
    let projectIds: [String]?
    let tagIds: [String]?

    enum CodingKeys: String, CodingKey {
        case projectIds = "project_ids"
        case tagIds = "tag_ids"
    }
}
