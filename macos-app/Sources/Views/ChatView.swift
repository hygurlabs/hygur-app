import SwiftUI

struct ChatView: View {
    @Bindable var viewModel: ChatViewModel
    @FocusState private var isInputFocused: Bool
    @State private var scrolledID: Message.ID?
    @State private var agendaViewModel = AgendaViewModel()
    @State private var showingAgendaSheet = false

    var body: some View {
        ZStack(alignment: .trailing) {
            chatArea

            // Context panel overlays from the right without pushing chat content
            if viewModel.isContextPanelVisible, let context = currentMessageContext {
                ContextPanelView(
                    context: context,
                    highlightedSourceIndex: viewModel.highlightedSourceIndex,
                    onSourceTap: { index in
                        viewModel.highlightSource(at: index)
                    }
                )
                .frame(width: 300)
                .frame(maxHeight: .infinity)
                .background(HygurColors.background)
                .hygurOverlayShadow()
                .transition(.move(edge: .trailing).combined(with: .opacity))
            }
        }
        .toolbar {
            ToolbarItem(placement: .automatic) {
                // Copy last assistant message button with keyboard shortcut
                Button {
                    viewModel.copyLastAssistantMessage()
                } label: {
                    Image(systemName: "doc.on.doc")
                }
                .keyboardShortcut("c", modifiers: [.command, .shift])
                .help("Copy last response (Cmd+Shift+C)")
                .disabled(viewModel.messages.last(where: { $0.role == .assistant }) == nil)
            }
            ToolbarItem(placement: .automatic) {
                if hasAnyRAGContext {
                    Button {
                        viewModel.toggleContextPanel()
                    } label: {
                        Image(systemName: viewModel.isContextPanelVisible ? "sidebar.trailing" : "sidebar.trailing")
                            .symbolVariant(viewModel.isContextPanelVisible ? .none : .slash)
                    }
                    .help(viewModel.isContextPanelVisible ? "Hide context panel" : "Show context panel")
                }
            }
        }
    }

    // MARK: - Chat Area

    private var chatArea: some View {
        VStack(spacing: 0) {
            if let label = viewModel.focusLabel {
                focusPill(label: label)
            }

            // Messages list
            ScrollView {
                LazyVStack(alignment: .leading, spacing: HygurSpacing.md) {
                    if viewModel.messages.isEmpty {
                        emptyStateView
                    } else {
                        ForEach(viewModel.messages) { message in
                            MessageBubble(
                                message: message,
                                isLastAssistantMessage: viewModel.isLastAssistantMessage(message),
                                isStreaming: viewModel.isStreaming,
                                isThinking: viewModel.isThinking && viewModel.isLastAssistantMessage(message),
                                streamStartTime: viewModel.isLastAssistantMessage(message) ? viewModel.streamStartTime : nil,
                                onCitationTap: { index in
                                    viewModel.highlightSource(at: index)
                                    if !viewModel.isContextPanelVisible {
                                        viewModel.toggleContextPanel()
                                    }
                                },
                                onCopy: {
                                    viewModel.copyMessage(message)
                                },
                                onRegenerate: {
                                    Task { await viewModel.regenerateLastResponse() }
                                }
                            )
                            .id(message.id)
                        }
                    }
                }
                .scrollTargetLayout()
                .padding()
            }
            .defaultScrollAnchor(.bottom)
            .scrollPosition(id: $scrolledID, anchor: .bottom)
            .id(viewModel.sessionId)
            .onChange(of: viewModel.messages.count) { _, _ in
                scrolledID = viewModel.messages.last?.id
            }
            .onChange(of: viewModel.messages.last?.content) { _, _ in
                scrolledID = viewModel.messages.last?.id
            }

            Divider()

            // Agenda badge — shown above the input when upcoming actions exist.
            if agendaViewModel.actions.count > 0 {
                HStack {
                    Button {
                        showingAgendaSheet = true
                    } label: {
                        Label(
                            "Focus: \(agendaViewModel.actions.count) action\(agendaViewModel.actions.count > 1 ? "s" : "")",
                            systemImage: "target"
                        )
                        .font(.caption)
                        .padding(.horizontal, 10)
                        .padding(.vertical, 4)
                        .background(Color.orange.opacity(0.15), in: Capsule())
                        .foregroundStyle(.orange)
                    }
                    .buttonStyle(.plain)
                    Spacer()
                }
                .padding(.horizontal, HygurSpacing.lg)
                .padding(.top, HygurSpacing.sm)
            }

            // Input area
            inputArea
        }
        .frame(minWidth: 400)
        .errorBannerOverlay($viewModel.error)
        .sheet(isPresented: $showingAgendaSheet) {
            AgendaSheet(actions: agendaViewModel.actions)
        }
        .task {
            await agendaViewModel.refresh()
        }
    }

    // MARK: - Focus Pill

    /// Shown above the chat area whenever the bound session has a project or
    /// tag scope. Visually communicates "retrieval is narrowed" so the user is
    /// never surprised by missing matches; tap × to drop the scope.
    private func focusPill(label: String) -> some View {
        HStack(spacing: HygurSpacing.sm) {
            Image(systemName: "scope")
                .font(.caption)
                .foregroundStyle(HygurColors.accent)
            Text("Filtered on \(label)")
                .font(.caption)
                .foregroundStyle(HygurColors.textSecondary)
            Spacer()
            Button {
                viewModel.clearFocus()
            } label: {
                Image(systemName: "xmark.circle.fill")
                    .foregroundStyle(HygurColors.textTertiary)
            }
            .buttonStyle(.plain)
            .help("Remove filter")
        }
        .padding(.horizontal, HygurSpacing.md)
        .padding(.vertical, HygurSpacing.sm)
        .background(HygurColors.surface)
        .overlay(
            Rectangle()
                .frame(height: 1)
                .foregroundStyle(Color.primary.opacity(0.05)),
            alignment: .bottom
        )
    }

    // MARK: - Empty State

    private var emptyStateView: some View {
        EmptyStateView(
            icon: "bubble.left.and.bubble.right",
            title: "Welcome to Hygur",
            subtitle: "Ask questions about your knowledge base"
        )
        .padding(.top, 100)
    }

    // MARK: - Input Area

    private var inputArea: some View {
        HStack(spacing: HygurSpacing.md) {
            TextField("Message...", text: $viewModel.inputText, axis: .vertical)
                .textFieldStyle(.plain)
                .lineLimit(1...5)
                .padding(HygurSpacing.md)
                .background(HygurColors.surface)
                .clipShape(RoundedRectangle(cornerRadius: HygurRadius.md))
                .focused($isInputFocused)
                .onSubmit {
                    Task { await viewModel.send() }
                }
                .disabled(viewModel.isStreaming)

            if viewModel.isStreaming {
                LoadingIndicator(style: viewModel.isThinking ? .thinking : .streaming)
                    .id(viewModel.isThinking)
                Button("Stop") {
                    viewModel.cancel()
                }
                .buttonStyle(.bordered)
            } else {
                Button {
                    Task { await viewModel.send() }
                } label: {
                    Image(systemName: "arrow.up.circle.fill")
                        .font(.title)
                        .foregroundStyle(HygurColors.accent)
                }
                .buttonStyle(.plain)
                .disabled(viewModel.inputText.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(HygurSpacing.lg)
    }

    // MARK: - Helpers

    /// Get the RAG context for the current/last assistant message
    private var currentMessageContext: RAGContext? {
        // First check if we're currently streaming with context
        if let current = viewModel.currentRAGContext {
            return current
        }
        // Otherwise, find the last assistant message with context
        return viewModel.messages.last(where: { $0.role == .assistant && $0.hasRAGContext })?.ragContext
    }

    /// Whether any message has RAG context
    private var hasAnyRAGContext: Bool {
        viewModel.currentRAGContext != nil ||
        viewModel.messages.contains(where: { $0.hasRAGContext })
    }
}

struct MessageBubble: View {
    let message: Message
    var isLastAssistantMessage: Bool = false
    var isStreaming: Bool = false
    var isThinking: Bool = false
    var streamStartTime: Date? = nil
    var onCitationTap: ((Int) -> Void)?
    var onCopy: (() -> Void)?
    var onRegenerate: (() -> Void)?

    @State private var isHovered: Bool = false

    var body: some View {
        HStack {
            if message.role == .user { Spacer(minLength: 60) }

            VStack(alignment: message.role == .user ? .trailing : .leading, spacing: 4) {
                ZStack(alignment: message.role == .user ? .topLeading : .topTrailing) {
                    messageContent
                        .padding(HygurSpacing.md)
                        .background(bubbleBackground)
                        .foregroundColor(message.role == .user ? .white : HygurColors.textPrimary)
                        .clipShape(RoundedRectangle(cornerRadius: HygurRadius.xl))

                    // Action buttons on hover (not during streaming, not for empty messages)
                    if isHovered && !isStreaming && !message.content.isEmpty {
                        MessageActionsView(
                            message: message,
                            isLastAssistantMessage: isLastAssistantMessage,
                            onCopy: { onCopy?() },
                            onRegenerate: onRegenerate
                        )
                        .offset(x: message.role == .user ? -8 : 8, y: -28)
                        .transition(AnyTransition.opacity.combined(with: AnyTransition.scale(scale: 0.9)))
                    }
                }
                .onHover { hovering in
                    withAnimation(.easeInOut(duration: 0.15)) {
                        isHovered = hovering
                    }
                }

                if message.content.isEmpty && message.role == .assistant {
                    if isThinking {
                        thinkingIndicator
                    } else {
                        typingIndicator
                    }
                }

                // Tool-call badges (inline) — surfaced before the RAG indicator
                // because they describe an action the assistant just took, which
                // is generally what the user wants to verify first.
                if let toolCalls = message.toolCalls, !toolCalls.isEmpty {
                    toolCallsView(toolCalls)
                }

                // RAG context indicator (inline)
                if message.hasRAGContext, let context = message.ragContext {
                    contextIndicator(sourceCount: context.sources.count)
                }

                // Generation stats below completed assistant messages
                if let stats = message.generationStats, message.role == .assistant {
                    generationStatsView(stats)
                }
            }

            if message.role == .assistant { Spacer(minLength: 60) }
        }
    }

    // MARK: - Message Content

    @ViewBuilder
    private var messageContent: some View {
        if message.role == .assistant {
            // Use markdown rendering for all assistant messages
            let sources = message.ragContext?.sources ?? []
            MarkdownMessageView(
                content: message.content,
                sources: sources,
                onCitationTap: onCitationTap
            )
            .textSelection(.enabled)
        } else {
            // Plain text for user and system messages
            Text(message.content)
                .textSelection(.enabled)
        }
    }

    // MARK: - Thinking Indicator

    private var thinkingIndicator: some View {
        TimelineView(.periodic(from: streamStartTime ?? Date(), by: 1.0)) { context in
            let elapsed = streamStartTime.map { Int(context.date.timeIntervalSince($0)) } ?? 0
            HStack(spacing: HygurSpacing.sm) {
                LoadingIndicator(style: .thinking)
                Text(elapsed > 0 ? "Thinking · \(elapsed)s" : "Thinking")
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textSecondary)
            }
            .padding(.horizontal, HygurSpacing.md)
            .padding(.vertical, HygurSpacing.xs)
        }
    }

    // MARK: - Typing Indicator

    private var typingIndicator: some View {
        TimelineView(.periodic(from: streamStartTime ?? Date(), by: 1.0)) { context in
            let elapsed = streamStartTime.map { Int(context.date.timeIntervalSince($0)) } ?? 0
            HStack(spacing: HygurSpacing.sm) {
                LoadingIndicator(style: .streaming)
                if elapsed > 0 {
                    Text("\(elapsed)s")
                        .font(.caption2)
                        .foregroundStyle(HygurColors.textSecondary.opacity(0.7))
                }
            }
            .padding(.horizontal, HygurSpacing.md)
        }
    }

    // MARK: - Generation Stats

    private func generationStatsView(_ stats: GenerationStats) -> some View {
        let parts = [stats.formattedDuration, stats.formattedTokens].compactMap { $0 }
        return Text(parts.joined(separator: " · "))
            .font(.caption2)
            .foregroundStyle(HygurColors.textSecondary.opacity(0.5))
            .padding(.horizontal, HygurSpacing.md)
    }

    // MARK: - Context Indicator

    private func contextIndicator(sourceCount: Int) -> some View {
        BadgeView(
            text: "\(sourceCount) source\(sourceCount == 1 ? "" : "s")",
            color: HygurColors.textSecondary,
            style: .capsule,
            icon: "doc.text.magnifyingglass"
        )
    }

    // MARK: - Tool Calls

    @ViewBuilder
    private func toolCallsView(_ calls: [ToolCall]) -> some View {
        let visible = calls.filter { !isToolCallSurfacedElsewhere($0) }
        if !visible.isEmpty {
            VStack(alignment: .leading, spacing: HygurSpacing.xs) {
                ForEach(visible) { call in
                    HStack(spacing: HygurSpacing.xs) {
                        Image(systemName: call.errorMessage == nil ? "checkmark.circle.fill" : "exclamationmark.triangle.fill")
                            .font(.caption)
                            .foregroundStyle(call.errorMessage == nil ? Color.green : Color.orange)
                            .accessibilityHidden(true)
                        Text(toolCallLabel(for: call))
                            .font(HygurTypography.caption)
                            .foregroundStyle(HygurColors.textSecondary)
                    }
                    .help(toolCallTooltip(for: call))
                }
            }
            .padding(.horizontal, HygurSpacing.md)
        }
    }

    private func isToolCallSurfacedElsewhere(_ call: ToolCall) -> Bool {
        // search_knowledge_base results are already shown via the RAG sources
        // panel (ragContext), so a duplicate chip just adds noise.
        call.name == "search_knowledge_base"
    }

    private func toolCallLabel(for call: ToolCall) -> String {
        switch call.name {
        case "create_note":
            return "Created a note"
        default:
            return "Used tool: \(call.name)"
        }
    }

    private func toolCallTooltip(for call: ToolCall) -> String {
        if let error = call.errorMessage {
            return "Error: \(error)"
        }
        return call.arguments
    }

    // MARK: - Bubble Background

    private var bubbleBackground: Color {
        switch message.role {
        case .user:
            return HygurColors.accent
        case .assistant:
            return HygurColors.surface
        case .system:
            return HygurColors.textSecondary.opacity(0.15)
        }
    }
}

#Preview {
    ChatView(viewModel: ChatViewModel())
}
