import Foundation

/// Wire shape for `GET /insights/learning-progress`. Mirrors
/// `interactions.LearningProgress` in the sidecar.
struct LearningProgressResponse: Codable, Sendable, Equatable {
    let coverage: Double
    let nextStep: String
    let nextStepHint: String
    let pillars: [LearningPillar]

    enum CodingKeys: String, CodingKey {
        case coverage
        case nextStep = "next_step"
        case nextStepHint = "next_step_hint"
        case pillars
    }
}

/// One axis of the learning-progress score.
struct LearningPillar: Codable, Sendable, Equatable, Identifiable {
    let key: String
    let label: String
    let progress: Double
    let current: Int
    let target: Int
    let weight: Double

    var id: String { key }
}
