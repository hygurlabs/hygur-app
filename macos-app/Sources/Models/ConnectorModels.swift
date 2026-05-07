import Foundation
import SwiftUI

// MARK: - Core Connector Models

struct ConnectorInfo: Codable, Sendable {
    let id: String
    let name: String
    let description: String
    let version: String
    let icon: String
    let color: String
    let tags: [String]
    let multiInstance: Bool

    enum CodingKeys: String, CodingKey {
        case id, name, description, version, icon, color, tags
        case multiInstance = "multi_instance"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decode(String.self, forKey: .name)
        description = (try? c.decodeIfPresent(String.self, forKey: .description)) ?? ""
        version = (try? c.decodeIfPresent(String.self, forKey: .version)) ?? ""
        icon = (try? c.decodeIfPresent(String.self, forKey: .icon)) ?? "puzzlepiece"
        color = (try? c.decodeIfPresent(String.self, forKey: .color)) ?? "#6366F1"
        tags = (try? c.decodeIfPresent([String].self, forKey: .tags)) ?? []
        multiInstance = (try? c.decodeIfPresent(Bool.self, forKey: .multiInstance)) ?? false
    }

    var accentColor: Color {
        Color(hex: color) ?? .accentColor
    }
}

// MARK: - Connector Instance (multi-compte)

struct ConnectorInstanceInfo: Codable, Identifiable, Sendable {
    let instanceID: String
    let typeID: String
    let displayName: String
    let info: ConnectorInfo
    let enabled: Bool
    let health: ConnectorHealth

    var id: String { instanceID }

    enum CodingKeys: String, CodingKey {
        case instanceID = "instance_id"
        case typeID = "type_id"
        case displayName = "display_name"
        case info, enabled, health
    }
}

struct ConnectorCapabilities: Codable, Sendable {
    let canList: Bool
    let canSearch: Bool
    let canSync: Bool
    let canIndex: Bool
    let canSummarize: Bool
    let canAttach: Bool
    let needsAuth: Bool
    let authType: String

    enum CodingKeys: String, CodingKey {
        case canList = "can_list"
        case canSearch = "can_search"
        case canSync = "can_sync"
        case canIndex = "can_index"
        case canSummarize = "can_summarize"
        case canAttach = "can_attach"
        case needsAuth = "needs_auth"
        case authType = "auth_type"
    }
}

struct ConnectorHealth: Codable, Sendable {
    let status: String
    let lastSync: String?
    let itemCount: Int
    let errorCount: Int
    let lastError: String
    let message: String
    /// Optional brief reason raw code (set by the mail connector). Empty for
    /// connectors that do not classify their errors.
    let briefReason: String

    enum CodingKeys: String, CodingKey {
        case status
        case lastSync = "last_sync"
        case itemCount = "item_count"
        case errorCount = "error_count"
        case lastError = "last_error"
        case message
        case briefReason = "brief_reason"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        status = try c.decode(String.self, forKey: .status)
        lastSync = try c.decodeIfPresent(String.self, forKey: .lastSync)
        itemCount = (try? c.decodeIfPresent(Int.self, forKey: .itemCount)) ?? 0
        errorCount = (try? c.decodeIfPresent(Int.self, forKey: .errorCount)) ?? 0
        lastError = (try? c.decodeIfPresent(String.self, forKey: .lastError)) ?? ""
        message = (try? c.decodeIfPresent(String.self, forKey: .message)) ?? ""
        briefReason = (try? c.decodeIfPresent(String.self, forKey: .briefReason)) ?? ""
    }

    var statusEnum: ConnectorStatus {
        ConnectorStatus(rawValue: status) ?? .unknown
    }

    /// Localized, user-facing reason. Falls back to a generic label when no
    /// brief reason has been classified (non-mail connectors).
    var briefReasonLocalized: String {
        if briefReason.isEmpty { return statusEnum.label }
        return BriefReason(rawValue: briefReason).localized
    }
}

// MARK: - Connector Status

enum ConnectorStatus: String, Codable, Sendable {
    case healthy
    case degraded
    case unhealthy
    case unconfigured
    case unknown

    var color: Color {
        switch self {
        case .healthy: return .green
        case .degraded: return .orange
        case .unhealthy: return .red
        case .unconfigured: return .secondary
        case .unknown: return .secondary
        }
    }

    var systemImage: String {
        switch self {
        case .healthy: return "checkmark.circle.fill"
        case .degraded: return "exclamationmark.triangle.fill"
        case .unhealthy: return "xmark.circle.fill"
        case .unconfigured: return "questionmark.circle"
        case .unknown: return "minus.circle"
        }
    }

    var label: String {
        switch self {
        case .healthy: return "Connected"
        case .degraded: return "Degraded"
        case .unhealthy: return "Error"
        case .unconfigured: return "Not configured"
        case .unknown: return "Unknown"
        }
    }
}

// MARK: - Connector Summary & Detail

struct ConnectorSummary: Codable, Identifiable, Sendable {
    let info: ConnectorInfo
    let enabled: Bool
    let health: ConnectorHealth

    var id: String { info.id }
}

struct ConnectorDetail: Codable, Sendable {
    let info: ConnectorInfo
    let capabilities: ConnectorCapabilities
    let configSchema: ConnectorConfigSchema
    var config: ConnectorConfig
    let health: ConnectorHealth

    enum CodingKeys: String, CodingKey {
        case info, capabilities, config, health
        case configSchema = "config_schema"
    }
}

// MARK: - Config Schema

struct ConnectorConfigSchema: Codable, Sendable {
    let groups: [ConnectorConfigGroup]
}

struct ConnectorConfigGroup: Codable, Sendable {
    let title: String
    let fields: [ConnectorConfigField]
}

struct ConnectorConfigField: Codable, Sendable {
    let key: String
    let fieldType: String
    let label: String
    let description: String
    let required: Bool
    let `default`: String
    let options: [ConnectorConfigOption]
    let condition: ConnectorConfigCondition?

    enum CodingKeys: String, CodingKey {
        case key, label, description, required, options, condition
        case fieldType = "type"
        case `default` = "default"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        key = try c.decode(String.self, forKey: .key)
        fieldType = try c.decode(String.self, forKey: .fieldType)
        label = try c.decode(String.self, forKey: .label)
        description = (try? c.decodeIfPresent(String.self, forKey: .description)) ?? ""
        required = (try? c.decodeIfPresent(Bool.self, forKey: .required)) ?? false
        `default` = (try? c.decodeIfPresent(String.self, forKey: .default)) ?? ""
        // Go serializes nil slice as null — default to empty array
        options = (try? c.decodeIfPresent([ConnectorConfigOption].self, forKey: .options)) ?? []
        condition = try? c.decodeIfPresent(ConnectorConfigCondition.self, forKey: .condition)
    }
}

struct ConnectorConfigOption: Codable, Sendable {
    let value: String
    let label: String
    let icon: String?
}

struct ConnectorConfigCondition: Codable, Sendable {
    let field: String
    let value: String
}

struct ConnectorConfig: Codable, Sendable {
    var enabled: Bool
    var settings: [String: String]
    var schedule: String

    enum CodingKeys: String, CodingKey {
        case enabled, settings, schedule
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        enabled = try c.decode(Bool.self, forKey: .enabled)
        settings = (try? c.decodeIfPresent([String: String].self, forKey: .settings)) ?? [:]
        schedule = (try? c.decodeIfPresent(String.self, forKey: .schedule)) ?? ""
    }

    init(enabled: Bool = false, settings: [String: String] = [:], schedule: String = "") {
        self.enabled = enabled
        self.settings = settings
        self.schedule = schedule
    }
}

// MARK: - Sync Result

struct SyncResult: Codable, Sendable {
    let processed: Int
    let skipped: Int
    let failed: Int
    let durationMs: Int

    enum CodingKeys: String, CodingKey {
        case processed, skipped, failed
        case durationMs = "duration_ms"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        processed = (try? c.decodeIfPresent(Int.self, forKey: .processed)) ?? 0
        skipped = (try? c.decodeIfPresent(Int.self, forKey: .skipped)) ?? 0
        failed = (try? c.decodeIfPresent(Int.self, forKey: .failed)) ?? 0
        durationMs = (try? c.decodeIfPresent(Int.self, forKey: .durationMs)) ?? 0
    }
}

// MARK: - Mailbox / Label responses

struct MailboxesResponse: Codable, Sendable {
    let mailboxes: [String]
}

struct MailboxOption: Codable, Sendable {
    let id: String
    let name: String
    let type: String
}

struct LabelsResponse: Codable, Sendable {
    let labels: [MailboxOption]
}

// MARK: - Marketplace

struct MarketplaceListing: Codable, Identifiable, Sendable {
    let id: String
    let typeName: String
    let displayName: String
    let description: String
    let version: String
    let author: String
    let iconName: String
    let iconColor: String
    let categories: [String]
    let capabilities: [String]
    let isBuiltIn: Bool
    let isInstalled: Bool
    let verified: Bool
    let multiInstance: Bool

    enum CodingKeys: String, CodingKey {
        case id
        case typeName = "type"
        case displayName = "display_name"
        case description, version, author
        case iconName = "icon_name"
        case iconColor = "icon_color"
        case categories, capabilities
        case isBuiltIn = "is_built_in"
        case isInstalled = "is_installed"
        case verified
        case multiInstance = "multi_instance"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        typeName = (try? c.decodeIfPresent(String.self, forKey: .typeName)) ?? id
        displayName = (try? c.decodeIfPresent(String.self, forKey: .displayName)) ?? id
        description = (try? c.decodeIfPresent(String.self, forKey: .description)) ?? ""
        version = (try? c.decodeIfPresent(String.self, forKey: .version)) ?? "1.0.0"
        author = (try? c.decodeIfPresent(String.self, forKey: .author)) ?? ""
        iconName = (try? c.decodeIfPresent(String.self, forKey: .iconName)) ?? "puzzlepiece"
        iconColor = (try? c.decodeIfPresent(String.self, forKey: .iconColor)) ?? "#6366F1"
        categories = (try? c.decodeIfPresent([String].self, forKey: .categories)) ?? []
        capabilities = (try? c.decodeIfPresent([String].self, forKey: .capabilities)) ?? []
        isBuiltIn = (try? c.decodeIfPresent(Bool.self, forKey: .isBuiltIn)) ?? true
        isInstalled = (try? c.decodeIfPresent(Bool.self, forKey: .isInstalled)) ?? false
        verified = (try? c.decodeIfPresent(Bool.self, forKey: .verified)) ?? false
        multiInstance = (try? c.decodeIfPresent(Bool.self, forKey: .multiInstance)) ?? false
    }

    var accentColor: Color {
        Color(hex: iconColor) ?? .accentColor
    }
}

// MARK: - Sidecar SSE Events

struct SidecarEvent: Codable, Sendable {
    let type: String      // "sync", "ingest", "mail", "connectors", "lm_studio", "priority_mail", "brief"
    let source: String    // connector ID or operation source / content_id for typed events
    let status: String    // "running", "completed", "failed", "pending"
    let message: String?
    let createdAt: String?
    let data: [String: SidecarEventValue]?

    enum CodingKeys: String, CodingKey {
        case type, source, status, message, data
        case createdAt = "created_at"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        type      = try c.decode(String.self, forKey: .type)
        source    = (try? c.decodeIfPresent(String.self, forKey: .source)) ?? ""
        status    = (try? c.decodeIfPresent(String.self, forKey: .status)) ?? ""
        message   = try? c.decodeIfPresent(String.self, forKey: .message)
        createdAt = try? c.decodeIfPresent(String.self, forKey: .createdAt)
        data      = try? c.decodeIfPresent([String: SidecarEventValue].self, forKey: .data)
    }

    /// Memberwise init for synthesizing app-side events (e.g. sidecar restart,
    /// chat failure) that aren't actually emitted by the sidecar over SSE.
    /// Lets us push those into the same Activity feed users already trust as
    /// the source of truth for what's happening.
    init(type: String,
         source: String = "app",
         status: String = "failed",
         message: String? = nil,
         createdAt: String? = nil,
         data: [String: SidecarEventValue]? = nil) {
        self.type = type
        self.source = source
        self.status = status
        self.message = message
        self.createdAt = createdAt
        self.data = data
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(type, forKey: .type)
        try c.encode(source, forKey: .source)
        try c.encode(status, forKey: .status)
        try c.encodeIfPresent(message, forKey: .message)
        try c.encodeIfPresent(createdAt, forKey: .createdAt)
        try c.encodeIfPresent(data, forKey: .data)
    }

    var isSyncRunning: Bool { type == "sync" && status == "running" }
    var isSyncDone: Bool    { type == "sync" && (status == "completed" || status == "failed") }

    // MARK: - Typed payload accessors for the new event types

    /// Convenience access to payload values, abstracting the JSON shape.
    func string(_ key: String) -> String? {
        guard case let .string(v)? = data?[key] else { return nil }
        return v
    }

    func int(_ key: String) -> Int? {
        switch data?[key] {
        case .int(let v)?: return v
        case .double(let v)?: return Int(v)
        default: return nil
        }
    }

    func bool(_ key: String) -> Bool? {
        guard case let .bool(v)? = data?[key] else { return nil }
        return v
    }

    func stringArray(_ key: String) -> [String]? {
        guard case let .array(values)? = data?[key] else { return nil }
        return values.compactMap { v in
            if case let .string(s) = v { return s }
            return nil
        }
    }

    /// Decode an array of `{content_id, one_liner}` dicts under `key`. Used
    /// by `mail_digest` events. Returns `nil` (not empty) when the key is
    /// missing so the caller can distinguish "no field" from "empty list".
    func digestItems(_ key: String = "items") -> [(contentId: String, oneLiner: String)]? {
        guard case let .array(values)? = data?[key] else { return nil }
        return values.compactMap { v in
            guard case let .dict(d) = v else { return nil }
            guard case let .string(cid)? = d["content_id"] else { return nil }
            guard case let .string(line)? = d["one_liner"] else { return nil }
            return (cid, line)
        }
    }
}

/// Heterogeneous value contained in `SidecarEvent.data`. The sidecar payload
/// helpers (Go-side `events.NewLMStudioEvent`, `NewPriorityMailEvent`,
/// `NewBriefEvent`) populate stable keys; this type just lets the Swift
/// side decode them without crashing on unexpected shapes.
enum SidecarEventValue: Codable, Sendable {
    case string(String)
    case int(Int)
    case double(Double)
    case bool(Bool)
    case array([SidecarEventValue])
    case dict([String: SidecarEventValue])
    case null

    init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() { self = .null; return }
        if let v = try? c.decode(Bool.self) { self = .bool(v); return }
        if let v = try? c.decode(Int.self) { self = .int(v); return }
        if let v = try? c.decode(Double.self) { self = .double(v); return }
        if let v = try? c.decode(String.self) { self = .string(v); return }
        if let v = try? c.decode([SidecarEventValue].self) { self = .array(v); return }
        if let v = try? c.decode([String: SidecarEventValue].self) { self = .dict(v); return }
        self = .null
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .string(let v): try c.encode(v)
        case .int(let v): try c.encode(v)
        case .double(let v): try c.encode(v)
        case .bool(let v): try c.encode(v)
        case .array(let v): try c.encode(v)
        case .dict(let v): try c.encode(v)
        case .null: try c.encodeNil()
        }
    }
}
