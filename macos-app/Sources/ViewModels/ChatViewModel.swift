import Foundation
import SwiftUI
import AppKit

@MainActor
@Observable
final class ChatViewModel {
    var messages: [Message] = []
    var inputText: String = ""
    var isStreaming: Bool = false
    var isThinking: Bool = false
    var error: String?
    var streamStartTime: Date? = nil

    /// Current RAG context for the active message (set before streaming)
    var currentRAGContext: RAGContext?

    /// Index of the highlighted source in the context panel
    var highlightedSourceIndex: Int?

    /// Whether the context panel is visible
    var isContextPanelVisible: Bool = false

    /// Current session ID (if bound to a session)
    private(set) var sessionId: UUID?

    /// Reference to session manager for persistence
    private var sessionManager: ChatSessionManager?

    /// Display label for the active focus scope (e.g. "Compta Q1"), surfaced
    /// as a pill in ChatView so the user always sees when retrieval is being
    /// narrowed by Mode Focus. Nil means unscoped — searches run across the
    /// full corpus.
    var focusLabel: String?

    private let sidecarService: SidecarService
    private var streamTask: Task<Void, Never>?
    private var focusLabelTask: Task<Void, Never>?

    init(sidecarService: SidecarService = .fromSettings()) {
        self.sidecarService = sidecarService
    }

    /// Bind to a session manager and session
    func bind(to sessionManager: ChatSessionManager, sessionId: UUID) {
        self.sessionManager = sessionManager
        self.sessionId = sessionId

        // Load messages from session
        if let session = sessionManager.session(for: sessionId) {
            self.messages = session.messages
        } else {
            self.messages = []
        }

        // Reset state
        self.inputText = ""
        self.error = nil
        self.currentRAGContext = nil
        self.highlightedSourceIndex = nil
        self.isStreaming = false
        self.isContextPanelVisible = false

        // Refresh focus label from the bound session.
        refreshFocusLabel()
    }

    /// Unbind from current session
    func unbind() {
        self.sessionManager = nil
        self.sessionId = nil
        self.messages = []
        self.inputText = ""
        self.error = nil
        self.currentRAGContext = nil
        self.isContextPanelVisible = false
        self.focusLabel = nil
        self.focusLabelTask?.cancel()
        self.focusLabelTask = nil
    }

    /// Drops the active focus scope from the bound session and clears the
    /// label. Future requests will run unscoped until the user picks a
    /// project/tag again from the sidebar.
    func clearFocus() {
        guard let sessionId else { return }
        sessionManager?.updateProject(nil, for: sessionId)
        sessionManager?.updateTags([], for: sessionId)
        focusLabel = nil
        focusLabelTask?.cancel()
        focusLabelTask = nil
    }

    /// Resolves the human-readable name for the bound session's focus scope
    /// (project name preferred, tag count as fallback) and writes it to
    /// `focusLabel`. Called from `bind()` and after focus changes.
    private func refreshFocusLabel() {
        focusLabelTask?.cancel()
        focusLabelTask = nil

        guard let sessionId,
              let session = sessionManager?.session(for: sessionId) else {
            focusLabel = nil
            return
        }

        if let projectId = session.projectId {
            // Optimistic placeholder while we fetch the real name.
            focusLabel = "Projet"
            focusLabelTask = Task { [sidecarService] in
                let project = try? await sidecarService.getProject(id: projectId)
                if Task.isCancelled { return }
                if let project {
                    self.focusLabel = project.name
                }
            }
            return
        }

        if !session.tagIds.isEmpty {
            focusLabel = session.tagIds.count == 1
                ? "1 tag"
                : "\(session.tagIds.count) tags"
            return
        }

        focusLabel = nil
    }

    // MARK: - Message Actions

    /// Copy message content to clipboard
    func copyMessage(_ message: Message) {
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        pasteboard.setString(message.content, forType: .string)
    }

    /// Copy the last assistant message to clipboard
    func copyLastAssistantMessage() {
        guard let lastAssistant = messages.last(where: { $0.role == .assistant }) else { return }
        copyMessage(lastAssistant)
    }

    /// Check if a message is the last assistant message
    func isLastAssistantMessage(_ message: Message) -> Bool {
        guard message.role == .assistant else { return false }
        return messages.last(where: { $0.role == .assistant })?.id == message.id
    }

    /// Regenerate the last assistant response
    func regenerateLastResponse() async {
        // Cancel any ongoing stream
        streamTask?.cancel()
        streamTask = nil
        isStreaming = false

        // Find the last assistant message index
        guard let lastAssistantIndex = messages.lastIndex(where: { $0.role == .assistant }) else { return }

        // Remove the last assistant message
        messages.remove(at: lastAssistantIndex)
        if let sessionId = sessionId {
            sessionManager?.removeLastMessage(from: sessionId)
        }

        // Clear any associated context
        currentRAGContext = nil
        highlightedSourceIndex = nil
        error = nil

        // Create empty assistant message for streaming
        let assistantMessage = Message(role: .assistant, content: "")
        messages.append(assistantMessage)
        let assistantIndex = messages.count - 1

        if let sessionId = sessionId {
            sessionManager?.addMessage(assistantMessage, to: sessionId)
        }

        isStreaming = true
        streamStartTime = Date()

        do {
            let history = Array(messages.dropLast())
            let chatMessages = buildChatMessages(from: history)
            let regenerateSourceIDs: [String] = history
                .filter { $0.role == .assistant }
                .suffix(2)
                .compactMap { $0.ragContext?.sources }
                .flatMap { $0 }
                .map { $0.contentId }
                .reduce(into: [String]()) { ids, id in
                    if !ids.contains(id) { ids.append(id) }
                }

            for try await event in await sidecarService.streamRAGChat(
                messages: chatMessages,
                recentSourceIDs: regenerateSourceIDs.isEmpty ? nil : regenerateSourceIDs,
                sessionId: sessionId?.uuidString,
                focusScope: currentFocusScope()
            ) {
                switch event {
                case .ragContext(let context):
                    currentRAGContext = context
                    messages[assistantIndex].ragContext = context
                    if !context.sources.isEmpty {
                        isContextPanelVisible = true
                    }

                case .delta(let delta):
                    messages[assistantIndex].content += delta
                    if let sessionId = sessionId {
                        sessionManager?.updateLastMessage(
                            in: sessionId,
                            content: messages[assistantIndex].content,
                            ragContext: messages[assistantIndex].ragContext
                        )
                    }

                case .done(let usage):
                    if let start = streamStartTime {
                        messages[assistantIndex].generationStats = GenerationStats(
                            duration: Date().timeIntervalSince(start),
                            completionTokens: usage?.completionTokens,
                            totalTokens: usage?.totalTokens
                        )
                    }

                case .error(let errorMessage):
                    self.error = errorMessage
                    if assistantIndex < messages.count && messages[assistantIndex].content.isEmpty {
                        messages.remove(at: assistantIndex)
                        if let sessionId = sessionId {
                            sessionManager?.removeLastMessage(from: sessionId)
                        }
                    }
                    currentRAGContext = nil
                }
            }
        } catch {
            self.error = error.localizedDescription
            if assistantIndex < messages.count && messages[assistantIndex].content.isEmpty {
                messages.remove(at: assistantIndex)
                if let sessionId = sessionId {
                    sessionManager?.removeLastMessage(from: sessionId)
                }
            }
            currentRAGContext = nil
        }

        isStreaming = false
        streamStartTime = nil

        if let sessionId = sessionId {
            sessionManager?.saveCurrentState(for: sessionId)
        }
    }

    /// Highlight a specific source in the context panel
    func highlightSource(at index: Int) {
        withAnimation(.easeInOut(duration: 0.2)) {
            highlightedSourceIndex = index
        }
        // Auto-clear highlight after a delay
        Task {
            try? await Task.sleep(nanoseconds: 2_000_000_000) // 2 seconds
            withAnimation {
                if highlightedSourceIndex == index {
                    highlightedSourceIndex = nil
                }
            }
        }
    }

    /// Toggle context panel visibility
    func toggleContextPanel() {
        withAnimation(.easeInOut(duration: 0.2)) {
            isContextPanelVisible.toggle()
        }
    }

    func send() async {
        guard !inputText.trimmingCharacters(in: .whitespaces).isEmpty else { return }

        // Cancel any ongoing stream
        streamTask?.cancel()
        streamTask = nil
        if isStreaming {
            isStreaming = false
        }

        let userMessage = Message(role: .user, content: inputText)
        messages.append(userMessage)

        // Sync to session
        if let sessionId = sessionId {
            sessionManager?.addMessage(userMessage, to: sessionId)
        }

        inputText = ""
        isStreaming = true
        isThinking = true
        streamStartTime = Date()
        error = nil
        currentRAGContext = nil
        highlightedSourceIndex = nil

        // Create empty assistant message for streaming
        let assistantMessage = Message(role: .assistant, content: "")
        messages.append(assistantMessage)
        let assistantIndex = messages.count - 1

        // Add assistant message to session (will be updated during streaming)
        if let sessionId = sessionId {
            sessionManager?.addMessage(assistantMessage, to: sessionId)
        }

        // Collect content_ids cited in the last 2 assistant turns for soft-boost.
        let recentSourceIDs: [String] = messages
            .dropLast()
            .filter { $0.role == .assistant }
            .suffix(2)
            .compactMap { $0.ragContext?.sources }
            .flatMap { $0 }
            .map { $0.contentId }
            .reduce(into: [String]()) { ids, id in
                if !ids.contains(id) { ids.append(id) }
            }

        // Store the task so it can be cancelled
        streamTask = Task {
            do {
                let chatMessages = buildChatMessages(from: Array(messages.dropLast()))

                for try await event in await sidecarService.streamRAGChat(
                    messages: chatMessages,
                    recentSourceIDs: recentSourceIDs.isEmpty ? nil : recentSourceIDs,
                    sessionId: sessionId?.uuidString,
                    focusScope: currentFocusScope()
                ) {
                    // Check for cancellation
                    if Task.isCancelled { break }

                    switch event {
                    case .ragContext(let context):
                        currentRAGContext = context
                        messages[assistantIndex].ragContext = context
                        if !context.sources.isEmpty {
                            isContextPanelVisible = true
                        }

                    case .delta(let delta):
                        isThinking = false
                        messages[assistantIndex].content += delta
                        if let sessionId = sessionId {
                            sessionManager?.updateLastMessage(
                                in: sessionId,
                                content: messages[assistantIndex].content,
                                ragContext: messages[assistantIndex].ragContext
                            )
                        }

                    case .done:
                        break

                    case .error(let errorMessage):
                        self.error = errorMessage
                        if assistantIndex < messages.count && messages[assistantIndex].content.isEmpty {
                            messages.remove(at: assistantIndex)
                            if let sessionId = sessionId {
                                sessionManager?.removeLastMessage(from: sessionId)
                            }
                        }
                        currentRAGContext = nil
                    }
                }
            } catch {
                if !Task.isCancelled {
                    self.error = error.localizedDescription
                    if assistantIndex < messages.count && messages[assistantIndex].content.isEmpty {
                        messages.remove(at: assistantIndex)
                        if let sessionId = sessionId {
                            sessionManager?.removeLastMessage(from: sessionId)
                        }
                    }
                    currentRAGContext = nil
                }
            }

            isStreaming = false
            isThinking = false

            if let sessionId = sessionId {
                sessionManager?.saveCurrentState(for: sessionId)
            }
        }

        // Wait for completion
        await streamTask?.value
    }

    // MARK: - Helpers

    /// Reads the bound session's projectId/tagIds and returns the matching
    /// FocusScopePayload, or nil when neither is set. Server-side empty equals
    /// nil — sending nil is preferred so the JSON omits the field entirely.
    func currentFocusScope() -> FocusScopePayload? {
        guard let sessionId,
              let session = sessionManager?.session(for: sessionId) else { return nil }
        return FocusScopePayload.from(projectId: session.projectId, tagIds: session.tagIds)
    }

    /// Builds the message list to send to the server, augmenting each assistant
    /// message with the titles of its RAG sources. This gives the server-side
    /// query-rewrite LLM concrete document names so follow-up questions like
    /// "show me the IBAN" produce queries anchored to the right document rather
    /// than any email that happens to contain an IBAN.
    private func buildChatMessages(from history: [Message]) -> [ChatMessage] {
        history.map { msg in
            guard msg.role == .assistant,
                  let sources = msg.ragContext?.sources,
                  !sources.isEmpty else {
                return ChatMessage(from: msg)
            }
            let titles = sources.prefix(3).map { $0.title }.joined(separator: "; ")
            return ChatMessage(
                role: msg.role.rawValue,
                content: msg.content + "\n[Documents used: \(titles)]"
            )
        }
    }

    func cancel() {
        streamTask?.cancel()
        streamTask = nil
        isStreaming = false
        isThinking = false
    }

    func clearMessages() {
        messages.removeAll()
        error = nil
    }
}
