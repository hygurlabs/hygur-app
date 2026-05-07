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

    /// Attachments queued for the next outgoing message. Populated by the
    /// paperclip picker, drag-drop, and clipboard paste, then drained when
    /// `send()` flushes them onto the user's `Message`.
    var pendingAttachments: [Attachment] = []

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
    /// Detached task that asks the sidecar to distill long-term memories from
    /// the current session. Runs after the assistant finishes streaming so it
    /// never blocks the chat UI. We hold a reference so a fresh send can
    /// supersede a still-running extraction (cheap — the latest state always
    /// wins; the sidecar handles the duplicate-content case).
    private var memoryExtractTask: Task<Void, Never>?

    /// Minimum number of user/assistant turns required before we bother the
    /// LLM with extraction. Below this threshold the sidecar's pre-filter
    /// would drop the transcript anyway — we save a round-trip.
    private static let memoryExtractMinTurns = 2

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
        self.pendingAttachments = []
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
        self.pendingAttachments = []
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

    // MARK: - Attachments

    /// Queue an image to be sent with the next outgoing message. The data
    /// is taken as-is — callers are responsible for transcoding if the
    /// source format isn't already one the runtime accepts (PNG/JPEG today).
    func addImage(data: Data, mimeType: String) {
        pendingAttachments.append(.image(data: data, mimeType: mimeType))
    }

    /// Queue an audio clip to be sent with the next outgoing message. The
    /// `format` is the OpenAI-spec short tag ("wav", "mp3"…) the runtime
    /// expects, not a MIME type. `duration` is metadata for the UI only.
    func addAudio(data: Data, format: String, duration: TimeInterval?) {
        pendingAttachments.append(.audio(data: data, format: format, duration: duration))
    }

    /// Drop a queued attachment by index, ignoring out-of-range requests so
    /// the UI doesn't have to guard against stale indices after rapid edits.
    func removePendingAttachment(at index: Int) {
        guard pendingAttachments.indices.contains(index) else { return }
        pendingAttachments.remove(at: index)
    }

    /// Backfill the duration on a queued audio attachment once it has been
    /// loaded asynchronously. Silently no-ops if the index is out of range
    /// or no longer points to an audio attachment (the user may have
    /// removed it before the duration resolved).
    func updatePendingAttachmentDuration(at index: Int, to duration: TimeInterval) {
        guard pendingAttachments.indices.contains(index) else { return }
        if case let .audio(data, format, _) = pendingAttachments[index] {
            pendingAttachments[index] = .audio(data: data, format: format, duration: duration)
        }
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

                case .toolCall(let call):
                    appendToolCall(call, to: assistantIndex, sessionId: sessionId)

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
                    if assistantIndex < messages.count && messages[assistantIndex].content.isEmpty && !messages[assistantIndex].hasToolCalls {
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

        // Phase 3.3 — distill long-term memories from the regenerated turn.
        // No-op when the transcript is too short or no session is bound.
        triggerMemoryExtraction()
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
        let trimmed = inputText.trimmingCharacters(in: .whitespaces)
        guard !trimmed.isEmpty || !pendingAttachments.isEmpty else { return }

        // Cancel any ongoing stream
        streamTask?.cancel()
        streamTask = nil
        if isStreaming {
            isStreaming = false
        }

        let attachmentsToSend = pendingAttachments
        let userMessage = Message(
            role: .user,
            content: inputText,
            attachments: attachmentsToSend.isEmpty ? nil : attachmentsToSend
        )
        messages.append(userMessage)

        // Sync to session
        if let sessionId = sessionId {
            sessionManager?.addMessage(userMessage, to: sessionId)
        }

        inputText = ""
        pendingAttachments = []
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

                    case .toolCall(let call):
                        isThinking = false
                        appendToolCall(call, to: assistantIndex, sessionId: sessionId)

                    case .done:
                        break

                    case .error(let errorMessage):
                        self.error = errorMessage
                        if assistantIndex < messages.count && messages[assistantIndex].content.isEmpty && !messages[assistantIndex].hasToolCalls {
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

            // Phase 3.3 — distill long-term memories from the just-completed
            // turn. Detached task; never blocks the chat UI. See
            // `triggerMemoryExtraction()` for the privacy contract (extracted
            // memories land as pending and require user review).
            triggerMemoryExtraction()
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

    /// Attach a streamed tool-call event to the in-flight assistant message.
    /// New ids are appended; an existing entry with the same id is updated in
    /// place — the sidecar is allowed to emit a tool_call event multiple
    /// times (e.g. once at request, once at result).
    private func appendToolCall(_ call: ToolCall, to assistantIndex: Int, sessionId: UUID?) {
        guard assistantIndex < messages.count else { return }
        var existing = messages[assistantIndex].toolCalls ?? []
        if let idx = existing.firstIndex(where: { $0.id == call.id }) {
            existing[idx] = call
        } else {
            existing.append(call)
        }
        messages[assistantIndex].toolCalls = existing
        if let sessionId = sessionId {
            sessionManager?.updateLastMessage(
                in: sessionId,
                content: messages[assistantIndex].content,
                ragContext: messages[assistantIndex].ragContext
            )
        }
    }

    func cancel() {
        streamTask?.cancel()
        streamTask = nil
        isStreaming = false
        isThinking = false
    }

    /// Fire-and-forget request to `/memory/extract` after a chat turn. The
    /// sidecar runs the LLM extractor on the transcript and persists any
    /// candidates with `source=extracted, accepted_at=NULL` — they appear in
    /// the "Pending review" section of `MemoriesView` but are NEVER injected
    /// into chat until the user clicks Accept. We send the transcript from
    /// the app because the sidecar's session store is in-memory and may not
    /// hold the full history when the call lands.
    ///
    /// Errors are swallowed: extraction is best-effort and an offline LM
    /// Studio shouldn't break the chat UX. Cancellation supersedes a
    /// still-running extraction so a fast follow-up turn doesn't pile work.
    private func triggerMemoryExtraction() {
        guard let sessionId else { return }
        // Pre-filter cheaply: < 2 turns has nothing extractable. Mirrors
        // the server-side guard in `ExtractMemoriesFromSession`.
        let conversational = messages.filter { $0.role == .user || $0.role == .assistant }
        guard conversational.count >= Self.memoryExtractMinTurns else { return }

        let payload = conversational.map {
            MemoryExtractMessage(role: $0.role.rawValue, content: $0.content)
        }
        let sid = sessionId.uuidString
        memoryExtractTask?.cancel()
        memoryExtractTask = Task { [sidecarService] in
            do {
                _ = try await sidecarService.extractMemories(sessionId: sid, messages: payload)
            } catch {
                // Best-effort — log via stderr but never surface to the chat
                // error banner. Failed extractions just mean no new pending
                // candidates this turn.
                #if DEBUG
                print("memory extract failed: \(error.localizedDescription)")
                #endif
            }
        }
    }

    func clearMessages() {
        messages.removeAll()
        pendingAttachments = []
        error = nil
    }
}
