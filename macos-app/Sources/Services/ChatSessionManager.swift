import Foundation

/// Manages chat session persistence and retrieval.
@Observable
final class ChatSessionManager {
    private(set) var sessions: [ChatSession] = []
    private(set) var isLoading = false
    var error: String?

    private let fileManager = FileManager.default
    private let encoder = JSONEncoder()
    private let decoder = JSONDecoder()

    init() {
        encoder.dateEncodingStrategy = .iso8601
        decoder.dateDecodingStrategy = .iso8601
        loadSessions()
    }

    // MARK: - Storage Path

    private var sessionsDirectoryURL: URL {
        let appSupport = fileManager.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
        let hygurDir = appSupport.appendingPathComponent("Hygur", isDirectory: true)
        let sessionsDir = hygurDir.appendingPathComponent("sessions", isDirectory: true)

        // Ensure directory exists
        try? fileManager.createDirectory(at: sessionsDir, withIntermediateDirectories: true)

        return sessionsDir
    }

    private func sessionFileURL(for id: UUID) -> URL {
        sessionsDirectoryURL.appendingPathComponent("\(id.uuidString).json")
    }

    // MARK: - CRUD Operations

    /// Load all sessions from disk
    func loadSessions() {
        isLoading = true
        defer { isLoading = false }

        do {
            let urls = try fileManager.contentsOfDirectory(
                at: sessionsDirectoryURL,
                includingPropertiesForKeys: [.contentModificationDateKey],
                options: .skipsHiddenFiles
            )

            var loadedSessions: [ChatSession] = []
            for url in urls where url.pathExtension == "json" {
                if let data = try? Data(contentsOf: url),
                   let session = try? decoder.decode(ChatSession.self, from: data) {
                    loadedSessions.append(session)
                }
            }

            // Sort by pinned first, then by updatedAt descending
            sessions = loadedSessions.sorted { lhs, rhs in
                if lhs.isPinned != rhs.isPinned {
                    return lhs.isPinned
                }
                return lhs.updatedAt > rhs.updatedAt
            }
        } catch {
            self.error = "Failed to load sessions: \(error.localizedDescription)"
        }
    }

    /// Create a new empty session
    @discardableResult
    func createSession(
        title: String = "New Chat",
        projectId: String? = nil,
        tagIds: [String] = []
    ) -> ChatSession {
        let session = ChatSession(
            title: title,
            projectId: projectId,
            tagIds: tagIds
        )
        sessions.insert(session, at: 0)
        saveSession(session)
        return session
    }

    /// Update an existing session
    func updateSession(_ session: ChatSession) {
        var updated = session
        updated.updatedAt = Date()

        if let index = sessions.firstIndex(where: { $0.id == session.id }) {
            sessions[index] = updated
        } else {
            sessions.insert(updated, at: 0)
        }

        // Re-sort
        sessions.sort { lhs, rhs in
            if lhs.isPinned != rhs.isPinned {
                return lhs.isPinned
            }
            return lhs.updatedAt > rhs.updatedAt
        }

        saveSession(updated)
    }

    /// Add a message to a session
    func addMessage(_ message: Message, to sessionId: UUID) {
        guard let index = sessions.firstIndex(where: { $0.id == sessionId }) else { return }

        sessions[index].messages.append(message)
        sessions[index].updatedAt = Date()

        saveSession(sessions[index])
    }

    /// Update the last message in a session (for streaming)
    func updateLastMessage(in sessionId: UUID, content: String, ragContext: RAGContext? = nil) {
        guard let index = sessions.firstIndex(where: { $0.id == sessionId }),
              !sessions[index].messages.isEmpty else { return }

        let lastIndex = sessions[index].messages.count - 1
        sessions[index].messages[lastIndex].content = content
        if let context = ragContext {
            sessions[index].messages[lastIndex].ragContext = context
        }
        // Don't save on every update during streaming - save when done
    }

    /// Remove last message from a session (for regeneration)
    func removeLastMessage(from sessionId: UUID) {
        guard let index = sessions.firstIndex(where: { $0.id == sessionId }),
              !sessions[index].messages.isEmpty else { return }

        sessions[index].messages.removeLast()
        sessions[index].updatedAt = Date()
        saveSession(sessions[index])
    }

    /// Delete a session
    func deleteSession(_ sessionId: UUID) {
        sessions.removeAll { $0.id == sessionId }

        let fileURL = sessionFileURL(for: sessionId)
        try? fileManager.removeItem(at: fileURL)
    }

    /// Toggle pin status
    func togglePin(for sessionId: UUID) {
        guard let index = sessions.firstIndex(where: { $0.id == sessionId }) else { return }

        sessions[index].isPinned.toggle()
        sessions[index].updatedAt = Date()

        // Re-sort
        sessions.sort { lhs, rhs in
            if lhs.isPinned != rhs.isPinned {
                return lhs.isPinned
            }
            return lhs.updatedAt > rhs.updatedAt
        }

        saveSession(sessions[index])
    }

    /// Update session title
    func updateTitle(_ title: String, for sessionId: UUID) {
        guard let index = sessions.firstIndex(where: { $0.id == sessionId }) else { return }

        sessions[index].title = title
        sessions[index].updatedAt = Date()
        saveSession(sessions[index])
    }

    /// Update session project
    func updateProject(_ projectId: String?, for sessionId: UUID) {
        guard let index = sessions.firstIndex(where: { $0.id == sessionId }) else { return }

        sessions[index].projectId = projectId
        sessions[index].updatedAt = Date()
        saveSession(sessions[index])
    }

    /// Update session tags
    func updateTags(_ tagIds: [String], for sessionId: UUID) {
        guard let index = sessions.firstIndex(where: { $0.id == sessionId }) else { return }

        sessions[index].tagIds = tagIds
        sessions[index].updatedAt = Date()
        saveSession(sessions[index])
    }

    /// Get a session by ID
    func session(for id: UUID) -> ChatSession? {
        sessions.first { $0.id == id }
    }

    // MARK: - Persistence

    private func saveSession(_ session: ChatSession) {
        do {
            let data = try encoder.encode(session)
            let url = sessionFileURL(for: session.id)
            try data.write(to: url, options: .atomic)
        } catch {
            self.error = "Failed to save session: \(error.localizedDescription)"
        }
    }

    /// Save current session state (call when streaming completes)
    func saveCurrentState(for sessionId: UUID) {
        guard let session = sessions.first(where: { $0.id == sessionId }) else { return }
        saveSession(session)
    }

    // MARK: - Filtering

    /// Filter sessions by project
    func sessions(forProject projectId: String) -> [ChatSession] {
        sessions.filter { $0.projectId == projectId }
    }

    /// Filter sessions by tag
    func sessions(withTag tagId: String) -> [ChatSession] {
        sessions.filter { $0.tagIds.contains(tagId) }
    }

    /// Search sessions by query
    func search(query: String) -> [ChatSession] {
        guard !query.isEmpty else { return sessions }

        let lowercased = query.lowercased()
        return sessions.filter { session in
            session.displayTitle.lowercased().contains(lowercased) ||
            session.messages.contains { $0.content.lowercased().contains(lowercased) }
        }
    }
}
