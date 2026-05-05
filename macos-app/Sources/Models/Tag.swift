import Foundation
import SwiftUI

/// Represents a tag with name and color for organizing knowledge items and notes.
struct Tag: Identifiable, Codable, Equatable, Sendable, Hashable {
    let id: String
    let name: String
    let color: String
    let usageCount: Int
    let createdAt: Date
    let updatedAt: Date

    enum CodingKeys: String, CodingKey {
        case id, name, color
        case usageCount = "usage_count"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }

    init(
        id: String,
        name: String,
        color: String,
        usageCount: Int = 0,
        createdAt: Date = Date(),
        updatedAt: Date = Date()
    ) {
        self.id = id
        self.name = name
        self.color = color
        self.usageCount = usageCount
        self.createdAt = createdAt
        self.updatedAt = updatedAt
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)

        id = try container.decode(String.self, forKey: .id)
        name = try container.decode(String.self, forKey: .name)
        color = try container.decode(String.self, forKey: .color)
        usageCount = try container.decodeIfPresent(Int.self, forKey: .usageCount) ?? 0

        // Parse ISO8601 dates (optional - TagSummary doesn't include them)
        let dateFormatter = ISO8601DateFormatter()
        dateFormatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]

        if let createdAtString = try container.decodeIfPresent(String.self, forKey: .createdAt) {
            if let date = dateFormatter.date(from: createdAtString) {
                createdAt = date
            } else {
                dateFormatter.formatOptions = [.withInternetDateTime]
                createdAt = dateFormatter.date(from: createdAtString) ?? Date()
            }
        } else {
            createdAt = Date()
        }

        if let updatedAtString = try container.decodeIfPresent(String.self, forKey: .updatedAt) {
            if let date = dateFormatter.date(from: updatedAtString) {
                updatedAt = date
            } else {
                dateFormatter.formatOptions = [.withInternetDateTime]
                updatedAt = dateFormatter.date(from: updatedAtString) ?? Date()
            }
        } else {
            updatedAt = Date()
        }
    }

    /// Convert hex color string to SwiftUI Color
    var swiftUIColor: Color {
        Color(hex: color) ?? .blue
    }
}

// MARK: - Request/Response Types

/// Request body for creating a new tag.
struct CreateTagRequest: Codable, Sendable {
    let name: String
    let color: String

    init(name: String, color: String) {
        self.name = name
        self.color = color
    }
}

/// Request body for updating an existing tag.
struct UpdateTagRequest: Codable, Sendable {
    let name: String?
    let color: String?

    init(name: String? = nil, color: String? = nil) {
        self.name = name
        self.color = color
    }
}

/// Response wrapper for tag list endpoint.
struct TagListResponse: Codable, Sendable {
    let tags: [Tag]
}

/// Represents a knowledge item in tag context.
struct TagItem: Identifiable, Codable, Equatable, Sendable {
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

/// Response wrapper for tag items endpoint.
struct TagItemsResponse: Codable, Sendable {
    let tagId: String
    let items: [TagItem]

    enum CodingKeys: String, CodingKey {
        case tagId = "tag_id"
        case items
    }
}

// MARK: - Color Extension

extension Color {
    /// Initialize Color from hex string (e.g., "#FF5733" or "FF5733")
    init?(hex: String) {
        var hexSanitized = hex.trimmingCharacters(in: .whitespacesAndNewlines)
        hexSanitized = hexSanitized.replacingOccurrences(of: "#", with: "")

        var rgb: UInt64 = 0

        guard Scanner(string: hexSanitized).scanHexInt64(&rgb) else {
            return nil
        }

        let length = hexSanitized.count
        if length == 6 {
            self.init(
                red: Double((rgb & 0xFF0000) >> 16) / 255.0,
                green: Double((rgb & 0x00FF00) >> 8) / 255.0,
                blue: Double(rgb & 0x0000FF) / 255.0
            )
        } else if length == 8 {
            self.init(
                red: Double((rgb & 0xFF000000) >> 24) / 255.0,
                green: Double((rgb & 0x00FF0000) >> 16) / 255.0,
                blue: Double((rgb & 0x0000FF00) >> 8) / 255.0,
                opacity: Double(rgb & 0x000000FF) / 255.0
            )
        } else {
            return nil
        }
    }

    /// Convert Color to hex string
    func toHex() -> String? {
        guard let components = NSColor(self).cgColor.components else {
            return nil
        }

        let r = components.count > 0 ? components[0] : 0
        let g = components.count > 1 ? components[1] : 0
        let b = components.count > 2 ? components[2] : 0

        return String(format: "#%02X%02X%02X",
                      Int(r * 255),
                      Int(g * 255),
                      Int(b * 255))
    }
}

// MARK: - Predefined Tag Colors

enum TagColor: String, CaseIterable, Identifiable {
    case red = "#E53935"
    case pink = "#D81B60"
    case purple = "#8E24AA"
    case deepPurple = "#5E35B1"
    case indigo = "#3949AB"
    case blue = "#1E88E5"
    case lightBlue = "#039BE5"
    case cyan = "#00ACC1"
    case teal = "#00897B"
    case green = "#43A047"
    case lightGreen = "#7CB342"
    case lime = "#C0CA33"
    case yellow = "#FDD835"
    case amber = "#FFB300"
    case orange = "#FB8C00"
    case deepOrange = "#F4511E"
    case brown = "#6D4C41"
    case grey = "#757575"
    case blueGrey = "#546E7A"

    var id: String { rawValue }

    var color: Color {
        Color(hex: rawValue) ?? .blue
    }

    var displayName: String {
        switch self {
        case .red: return "Red"
        case .pink: return "Pink"
        case .purple: return "Purple"
        case .deepPurple: return "Deep Purple"
        case .indigo: return "Indigo"
        case .blue: return "Blue"
        case .lightBlue: return "Light Blue"
        case .cyan: return "Cyan"
        case .teal: return "Teal"
        case .green: return "Green"
        case .lightGreen: return "Light Green"
        case .lime: return "Lime"
        case .yellow: return "Yellow"
        case .amber: return "Amber"
        case .orange: return "Orange"
        case .deepOrange: return "Deep Orange"
        case .brown: return "Brown"
        case .grey: return "Grey"
        case .blueGrey: return "Blue Grey"
        }
    }
}
