import Foundation
import SwiftUI

// MARK: - Graph Data Models

/// Represents a node in the knowledge graph.
struct GraphNode: Identifiable, Codable, Equatable, @unchecked Sendable {
    let id: String
    let type: GraphNodeType
    let label: String
    let color: String?
    let sourceType: String?
    let sourcePath: String?
    let metadata: [String: GraphNodeValue]?
    let createdAt: String?
    let updatedAt: String?

    enum CodingKeys: String, CodingKey {
        case id, type, label, color
        case sourceType = "source_type"
        case sourcePath = "source_path"
        case metadata
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }

    var displayColor: Color {
        if let hex = color {
            return Color(hex: hex) ?? nodeTypeColor
        }
        return nodeTypeColor
    }

    var nodeTypeColor: Color {
        switch type {
        case .tag: return .purple
        case .item: return itemSourceColor
        case .project: return .orange
        }
    }

    private var itemSourceColor: Color {
        switch sourceType {
        case "note": return .blue
        case "mail", "email": return .red
        case "file", "markdown", "md": return .green
        case "pdf": return .brown
        default: return .gray
        }
    }

    var icon: String {
        switch type {
        case .tag: return "tag.fill"
        case .project: return "folder.fill"
        case .item:
            switch sourceType {
            case "note": return "note.text"
            case "mail", "email": return "envelope.fill"
            case "markdown", "md": return "doc.text"
            case "pdf": return "doc.richtext"
            default: return "doc"
            }
        }
    }

    var nodeSize: CGFloat {
        switch type {
        case .tag: return 20
        case .project: return 24
        case .item: return 16
        }
    }
}

/// Type of graph node.
enum GraphNodeType: String, Codable {
    case tag
    case item
    case project
}

/// Represents an edge between two nodes.
struct GraphEdge: Codable, Equatable, Sendable {
    let source: String
    let target: String
    let type: String
}

/// Response from the graph endpoint.
struct GraphResponse: Codable, Sendable {
    let nodes: [GraphNode]
    let edges: [GraphEdge]
}

// MARK: - GraphNodeValue for metadata

struct GraphNodeValue: Codable, Equatable {
    let value: Any

    init(_ value: Any) {
        self.value = value
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let bool = try? container.decode(Bool.self) {
            value = bool
        } else if let int = try? container.decode(Int.self) {
            value = int
        } else if let double = try? container.decode(Double.self) {
            value = double
        } else if let string = try? container.decode(String.self) {
            value = string
        } else if let array = try? container.decode([GraphNodeValue].self) {
            value = array.map { $0.value }
        } else if let dict = try? container.decode([String: GraphNodeValue].self) {
            value = dict.mapValues { $0.value }
        } else {
            value = NSNull()
        }
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch value {
        case let bool as Bool:
            try container.encode(bool)
        case let int as Int:
            try container.encode(int)
        case let double as Double:
            try container.encode(double)
        case let string as String:
            try container.encode(string)
        default:
            try container.encodeNil()
        }
    }

    static func == (lhs: GraphNodeValue, rhs: GraphNodeValue) -> Bool {
        String(describing: lhs.value) == String(describing: rhs.value)
    }
}
