import AppKit
import Foundation

/// Writes chat sessions and notes to Markdown files via `NSSavePanel`.
///
/// Format: YAML front-matter (metadata only — never user content) followed by
/// the rendered body. Notes are emitted as-is since their content already lives
/// in Markdown; chat sessions are reflowed into role-prefixed sections with an
/// optional sources block when RAG context is present.
@MainActor
enum MarkdownExportService {
    enum ExportError: LocalizedError {
        case userCancelled
        case write(Error)

        var errorDescription: String? {
            switch self {
            case .userCancelled: return nil
            case .write(let error): return "Could not write file: \(error.localizedDescription)"
            }
        }
    }

    // MARK: - Chat sessions

    /// Prompt the user for a destination and write the rendered session to disk.
    /// Returns the saved URL on success; throws `userCancelled` if the panel is
    /// dismissed without a target.
    @discardableResult
    static func exportChatSession(_ session: ChatSession) throws -> URL {
        let body = renderSession(session)
        return try save(content: body, defaultName: defaultFilename(for: session))
    }

    static func renderSession(_ session: ChatSession) -> String {
        var lines: [String] = []
        lines.append(frontMatterForSession(session))
        lines.append("")
        lines.append("# \(session.displayTitle)")
        lines.append("")

        let formatter = DateFormatter()
        formatter.dateFormat = "yyyy-MM-dd HH:mm"

        for message in session.messages {
            switch message.role {
            case .user:
                lines.append("## You — \(formatter.string(from: message.timestamp))")
            case .assistant:
                lines.append("## Assistant — \(formatter.string(from: message.timestamp))")
            case .system:
                // System prompts are internal; surface them under a clear label
                // so the file stays self-explanatory.
                lines.append("## System — \(formatter.string(from: message.timestamp))")
            }
            lines.append("")
            lines.append(message.content)
            lines.append("")

            if let context = message.ragContext, !context.sources.isEmpty {
                lines.append("### Sources")
                lines.append("")
                for (index, source) in context.sources.enumerated() {
                    let scorePart = source.score > 0 ? " · \(source.scorePercentage)" : ""
                    let title = source.title.isEmpty ? source.contentId : source.title
                    lines.append("\(index + 1). **\(title)** (\(source.sourceLabel)\(scorePart))")
                }
                lines.append("")
            }
        }

        return lines.joined(separator: "\n")
    }

    // MARK: - Notes

    @discardableResult
    static func exportNote(_ note: Note) throws -> URL {
        let body = renderNote(note)
        return try save(content: body, defaultName: defaultFilename(for: note))
    }

    static func renderNote(_ note: Note) -> String {
        var lines: [String] = []
        lines.append(frontMatterForNote(note))
        lines.append("")
        // The title duplicates the front-matter on purpose — a Markdown reader
        // that ignores YAML still gets a heading.
        lines.append("# \(note.title)")
        lines.append("")
        lines.append(note.content)
        return lines.joined(separator: "\n")
    }

    // MARK: - Front-matter

    private static func frontMatterForSession(_ session: ChatSession) -> String {
        var lines: [String] = ["---"]
        lines.append("title: \(yamlString(session.displayTitle))")
        lines.append("created: \(iso8601(session.createdAt))")
        lines.append("updated: \(iso8601(session.updatedAt))")
        lines.append("exported: \(iso8601(Date()))")
        lines.append("messages: \(session.messages.count)")
        if let projectId = session.projectId {
            lines.append("project: \(yamlString(projectId))")
        }
        if !session.tagIds.isEmpty {
            let joined = session.tagIds.map(yamlString).joined(separator: ", ")
            lines.append("tags: [\(joined)]")
        }
        lines.append("source: hygur")
        lines.append("---")
        return lines.joined(separator: "\n")
    }

    private static func frontMatterForNote(_ note: Note) -> String {
        var lines: [String] = ["---"]
        lines.append("title: \(yamlString(note.title))")
        lines.append("created: \(iso8601(note.createdAt))")
        lines.append("updated: \(iso8601(note.updatedAt))")
        lines.append("exported: \(iso8601(Date()))")
        if let projectId = note.projectId {
            lines.append("project: \(yamlString(projectId))")
        }
        if !note.tags.isEmpty {
            let joined = note.tags.map { yamlString($0.name) }.joined(separator: ", ")
            lines.append("tags: [\(joined)]")
        }
        lines.append("source: hygur")
        lines.append("---")
        return lines.joined(separator: "\n")
    }

    // MARK: - Filename helpers

    private static func defaultFilename(for session: ChatSession) -> String {
        let stamp = filenameDate(session.updatedAt)
        return "\(safeFilename(session.displayTitle)) — \(stamp).md"
    }

    private static func defaultFilename(for note: Note) -> String {
        let stamp = filenameDate(note.updatedAt)
        return "\(safeFilename(note.title)) — \(stamp).md"
    }

    private static func filenameDate(_ date: Date) -> String {
        let formatter = DateFormatter()
        formatter.dateFormat = "yyyy-MM-dd"
        return formatter.string(from: date)
    }

    /// Replace POSIX-unfriendly characters and trim length so the panel doesn't
    /// reject the suggested name. Keeps spaces and accented characters.
    private static func safeFilename(_ raw: String) -> String {
        let stripped = raw
            .replacingOccurrences(of: "/", with: "-")
            .replacingOccurrences(of: ":", with: "-")
            .replacingOccurrences(of: "\\", with: "-")
            .trimmingCharacters(in: .whitespacesAndNewlines)
        let trimmed = stripped.isEmpty ? "Untitled" : stripped
        return String(trimmed.prefix(80))
    }

    // MARK: - YAML / ISO formatting

    private static func iso8601(_ date: Date) -> String {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime]
        return formatter.string(from: date)
    }

    /// Quote any value that could break the YAML parser (newlines, leading
    /// punctuation, special characters). Plain ASCII titles round-trip without
    /// quotes for readability.
    private static func yamlString(_ raw: String) -> String {
        let needsQuoting = raw.contains(":") || raw.contains("#") || raw.contains("\n")
            || raw.contains("\"") || raw.hasPrefix(" ") || raw.hasSuffix(" ")
            || raw.isEmpty
        guard needsQuoting else { return raw }
        let escaped = raw
            .replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "\"", with: "\\\"")
            .replacingOccurrences(of: "\n", with: "\\n")
        return "\"\(escaped)\""
    }

    // MARK: - Save panel

    private static func save(content: String, defaultName: String) throws -> URL {
        let panel = NSSavePanel()
        panel.title = "Export to Markdown"
        panel.allowedContentTypes = [.init(filenameExtension: "md") ?? .plainText]
        panel.nameFieldStringValue = defaultName
        panel.canCreateDirectories = true
        panel.isExtensionHidden = false

        let response = panel.runModal()
        guard response == .OK, let url = panel.url else {
            throw ExportError.userCancelled
        }

        do {
            try content.write(to: url, atomically: true, encoding: .utf8)
            return url
        } catch {
            throw ExportError.write(error)
        }
    }
}
