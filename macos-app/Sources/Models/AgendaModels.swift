import Foundation

struct AgendaAction: Identifiable, Codable, Sendable {
    let what: String
    let deadlineISO: String
    let priority: String
    let sourceId: String
    let confidence: Double

    var id: String { "\(sourceId)@\(deadlineISO)" }

    enum CodingKeys: String, CodingKey {
        case what
        case deadlineISO = "deadline_iso"
        case priority
        case sourceId = "source_id"
        case confidence
    }
}

struct AgendaContextResponse: Codable, Sendable {
    let actions: [AgendaAction]
    let generatedAt: String

    enum CodingKeys: String, CodingKey {
        case actions
        case generatedAt = "generated_at"
    }
}
