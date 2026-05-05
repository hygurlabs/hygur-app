import Foundation
import SwiftUI

// MARK: - RAG Context

/// Context returned by the RAG Chat Handler before streaming begins
struct RAGContext: Codable, Sendable, Equatable {
    let sources: [RAGSource]
    let intent: RAGIntent?
}

// MARK: - RAG Source

/// A source document or email used for RAG context
struct RAGSource: Codable, Identifiable, Sendable, Equatable {
    var id: String { contentId }

    let contentId: String
    let sourceType: String
    let title: String
    let excerpt: String
    let score: Double

    // Mail-specific fields (optional)
    let mailFrom: String?
    let mailDate: String?
    let mailSubject: String?

    enum CodingKeys: String, CodingKey {
        case contentId = "content_id"
        case sourceType = "source_type"
        case title, excerpt, score
        case mailFrom = "mail_from"
        case mailDate = "mail_date"
        case mailSubject = "mail_subject"
    }

    // MARK: - Helper Properties

    /// SF Symbol icon for the source type
    var icon: String {
        switch sourceType.lowercased() {
        case "email", "mail":
            return "envelope.fill"
        case "document", "doc", "file":
            return "doc.text.fill"
        case "pdf":
            return "doc.richtext.fill"
        case "markdown", "md":
            return "text.document.fill"
        case "code":
            return "chevron.left.forwardslash.chevron.right"
        case "note":
            return "note.text"
        case "web", "url":
            return "globe"
        default:
            return "doc.fill"
        }
    }

    /// Color associated with the source type
    var color: Color {
        switch sourceType.lowercased() {
        case "email", "mail":
            return .blue
        case "document", "doc", "file":
            return .orange
        case "pdf":
            return .red
        case "markdown", "md":
            return .purple
        case "code":
            return .green
        case "note":
            return .yellow
        case "web", "url":
            return .cyan
        default:
            return .secondary
        }
    }

    /// Human-readable label for the source type
    var sourceLabel: String {
        switch sourceType.lowercased() {
        case "email", "mail":
            return "Email"
        case "document", "doc":
            return "Document"
        case "file":
            return "File"
        case "pdf":
            return "PDF"
        case "markdown", "md":
            return "Markdown"
        case "code":
            return "Code"
        case "note":
            return "Note"
        case "web", "url":
            return "Web"
        default:
            return sourceType.capitalized
        }
    }

    /// Formatted relevance score as percentage
    var scorePercentage: String {
        String(format: "%.0f%%", score * 100)
    }

    /// Indicates if this source is an email
    var isEmail: Bool {
        sourceType.lowercased() == "email" || sourceType.lowercased() == "mail"
    }
}

// MARK: - RAG Intent

/// The interpreted intent from the user query
struct RAGIntent: Codable, Sendable, Equatable {
    let query: String
    let confidence: Double
    let weights: [String: Double]?

    /// Formatted confidence as percentage
    var confidencePercentage: String {
        String(format: "%.0f%%", confidence * 100)
    }
}

// MARK: - RAG SSE Event

/// SSE event containing RAG context
struct RAGContextEvent: Codable, Sendable {
    let type: String
    let sources: [RAGSource]
    let intent: RAGIntent?
}
