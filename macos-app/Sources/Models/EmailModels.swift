import Foundation

// MARK: - Email Source

struct EmailSource: Identifiable, Codable, Sendable {
    var id: String { name }
    let name: String
    let status: String
    let error: String?
}

// MARK: - Mail Account (multi-account)

/// Public, user-facing view of a configured mail account. Mirrors the JSON
/// shape returned by `GET /mail/accounts`.
struct MailAccount: Identifiable, Codable, Sendable, Hashable {
    let accountId: String
    let provider: String       // "gmail" | "proton"
    let email: String
    let status: String         // "connected" | "disconnected" | "error"
    let briefReason: String    // BriefReason raw code, e.g. "ok", "auth_issue"
    let threadCount: Int
    let lastSync: String?
    let lastVerified: String?
    let lastIndexed: String?

    var id: String { accountId }

    /// Convenience: localized brief reason used by views to render the dot
    /// label without parsing raw codes.
    var briefReasonLocalized: String { BriefReason(rawValue: briefReason).localized }

    /// True if the account is in a connectable state.
    var isConnected: Bool { status == "connected" }

    enum CodingKeys: String, CodingKey {
        case accountId = "account_id"
        case provider, email, status
        case briefReason = "brief_reason"
        case threadCount = "thread_count"
        case lastSync = "last_sync"
        case lastVerified = "last_verified"
        case lastIndexed = "last_indexed"
    }
}

struct MailAccountsResponse: Codable, Sendable {
    let accounts: [MailAccount]
}

/// Response of `POST /mail/accounts/{id}/verify`.
struct MailAccountVerifyResponse: Codable, Sendable {
    let accountId: String
    let status: String
    let briefReason: String
    let lastVerified: String?

    var briefReasonLocalized: String { BriefReason(rawValue: briefReason).localized }

    enum CodingKeys: String, CodingKey {
        case accountId = "account_id"
        case status
        case briefReason = "brief_reason"
        case lastVerified = "last_verified"
    }
}

struct MailAccountStatsResponse: Codable, Sendable {
    let accountId: String
    let threadCount: Int
    let lastIndexed: String?

    enum CodingKeys: String, CodingKey {
        case accountId = "account_id"
        case threadCount = "thread_count"
        case lastIndexed = "last_indexed"
    }
}

/// Sync trigger ack (async sync). Returned by `POST /connectors/mail/sync?async=true`.
struct MailSyncAck: Codable, Sendable {
    let status: String
    let jobId: String?
    let message: String?

    enum CodingKeys: String, CodingKey {
        case status
        case jobId = "job_id"
        case message
    }
}

// MARK: - Brief Reason

/// Stable codes shared with the Go classifier (`internal/mail/diag.BriefReason`).
/// Add new cases here in lockstep with the backend so the UI never displays
/// a raw error string.
enum BriefReason: String, Sendable {
    case ok
    case authIssue = "auth_issue"
    case networkIssue = "network_issue"
    case rateLimited = "rate_limited"
    case notConfigured = "not_configured"
    case unknownIssue = "unknown_issue"

    init(rawValue: String) {
        self = BriefReason(rawCode: rawValue) ?? .unknownIssue
    }

    private init?(rawCode: String) {
        switch rawCode {
        case "ok": self = .ok
        case "auth_issue": self = .authIssue
        case "network_issue": self = .networkIssue
        case "rate_limited": self = .rateLimited
        case "not_configured": self = .notConfigured
        case "unknown_issue", "": self = .unknownIssue
        default: return nil
        }
    }

    /// User-facing label.
    var localized: String {
        switch self {
        case .ok: return "Connected"
        case .authIssue: return "Authentication issue"
        case .networkIssue: return "Network connection issue"
        case .rateLimited: return "Rate limit reached"
        case .notConfigured: return "Not configured"
        case .unknownIssue: return "Internal error"
        }
    }

    /// Whether this reason should render as a green status dot.
    var isHealthy: Bool { self == .ok }
}

// MARK: - Email Thread

struct EmailThread: Identifiable, Codable, Sendable {
    let id: String
    let subject: String
    let participants: [String]
    let messageCount: Int
    let hasAttachments: Bool
    let dateStart: String
    let dateEnd: String

    enum CodingKeys: String, CodingKey {
        case id, subject, participants
        case messageCount = "message_count"
        case hasAttachments = "has_attachments"
        case dateStart = "date_start"
        case dateEnd = "date_end"
    }
}

// MARK: - Email Summary

struct EmailSummary: Codable, Sendable {
    let summaryId: String
    let decisions: [String]
    let actions: [String]
    let openQuestions: [String]

    enum CodingKeys: String, CodingKey {
        case summaryId = "summary_id"
        case decisions, actions
        case openQuestions = "open_questions"
    }
}

// MARK: - API Request/Response Types

struct MailSourcesResponse: Codable, Sendable {
    let sources: [EmailSource]
}

struct MailThreadsResponse: Codable, Sendable {
    let threads: [EmailThread]
    let total: Int?
}

struct ConnectMailSourceRequest: Codable, Sendable {
    let source: String
    let username: String?
    let password: String?
    let token: String?
}

struct IndexMailThreadRequest: Codable, Sendable {
    let source: String
    let threadId: String

    enum CodingKeys: String, CodingKey {
        case source
        case threadId = "thread_id"
    }
}

struct SummarizeMailThreadRequest: Codable, Sendable {
    let source: String
    let model: String
}

// MARK: - Mailbox Indexing

struct IndexMailboxRequest: Codable, Sendable {
    let source: String
    let mailbox: String
    let limit: Int?
    let batchSize: Int?
    let maxConcurrent: Int?

    enum CodingKeys: String, CodingKey {
        case source, mailbox, limit
        case batchSize = "batch_size"
        case maxConcurrent = "max_concurrent"
    }
}

struct IndexMailboxResponse: Codable, Sendable {
    let totalThreads: Int
    let processedThreads: Int
    let indexedMessages: Int
    let skippedDuplicates: Int
    let updatedThreads: Int
    let errors: Int
    let errorMessages: [String]?
    let durationSeconds: Double

    enum CodingKeys: String, CodingKey {
        case totalThreads = "total_threads"
        case processedThreads = "processed_threads"
        case indexedMessages = "indexed_messages"
        case skippedDuplicates = "skipped_duplicates"
        case updatedThreads = "updated_threads"
        case errors
        case errorMessages = "error_messages"
        case durationSeconds = "duration_seconds"
    }
}

// MARK: - Mail Labels

struct MailLabel: Identifiable, Codable, Sendable {
    var id: String { labelId }
    let labelId: String
    let name: String
    let type: String

    enum CodingKeys: String, CodingKey {
        case labelId = "id"
        case name, type
    }
}

struct MailLabelsResponse: Codable, Sendable {
    let labels: [MailLabel]
}
