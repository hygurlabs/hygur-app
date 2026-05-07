import SwiftUI
import AppKit
import AVFoundation
import UniformTypeIdentifiers

struct ChatView: View {
    @Bindable var viewModel: ChatViewModel
    @State private var scrolledID: Message.ID?
    @State private var agendaViewModel = AgendaViewModel()
    @State private var showingAgendaSheet = false
    /// VoiceService is owned by HygurApp and injected via Environment —
    /// using a per-ChatView @State instance caused a multi-instance race
    /// where prepare() pre-warmed instance A but the click landed on
    /// instance B, triggering the synchronous Speech framework load.
    @Environment(VoiceService.self) private var voiceService
    @State private var speechService = SpeechService()
    @AppStorage("voice.autoSpeak") private var autoSpeakResponses: Bool = false
    /// How many characters of the streaming assistant message we've already
    /// fed to the synthesizer, keyed by message id. Lets `feed(_:)` receive
    /// only the new tail on each content update.
    @State private var spokenLengths: [UUID: Int] = [:]
    /// Message currently being read aloud by a manual click. Cleared when
    /// the synthesizer goes idle or the user clicks again to stop.
    @State private var manuallySpokenMessageId: UUID?

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
                Button {
                    autoSpeakResponses.toggle()
                    if !autoSpeakResponses { speechService.stop() }
                } label: {
                    Image(systemName: autoSpeakResponses ? "speaker.wave.2.fill" : "speaker.slash")
                }
                .help(autoSpeakResponses ? "Auto-read replies (on)" : "Auto-read replies (off)")
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
                                },
                                isSpeaking: manuallySpokenMessageId == message.id,
                                onSpeak: message.role == .assistant ? {
                                    toggleSpeak(message)
                                } : nil
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
                feedAutoSpeakIfNeeded()
            }
            .onChange(of: viewModel.isStreaming) { wasStreaming, nowStreaming in
                if wasStreaming && !nowStreaming && autoSpeakResponses {
                    speechService.finish()
                }
            }
            .onChange(of: speechService.isSpeaking) { _, nowSpeaking in
                if !nowSpeaking { manuallySpokenMessageId = nil }
            }

            Divider()

            // Agenda badge — surfaced above the input when actions OR
            // calendar events exist, so users can always reach the sheet
            // (events alone are enough; actions alone are enough; both is
            // best). The label adapts to whichever signals are present.
            if agendaViewModel.actions.count > 0 || agendaViewModel.calendarEvents.count > 0 {
                HStack {
                    Button {
                        showingAgendaSheet = true
                    } label: {
                        Label(agendaBadgeLabel, systemImage: agendaBadgeSymbol)
                            .font(.caption)
                            .padding(.horizontal, 10)
                            .padding(.vertical, 4)
                            .background(agendaBadgeTint.opacity(0.15), in: Capsule())
                            .foregroundStyle(agendaBadgeTint)
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
            AgendaSheet(viewModel: agendaViewModel)
        }
        .sheet(item: $viewModel.pendingCalendarEventProposal) { proposal in
            CreateCalendarEventSheet(
                initial: proposal,
                onConfirm: { _ in viewModel.pendingCalendarEventProposal = nil },
                onCancel: { viewModel.pendingCalendarEventProposal = nil }
            )
        }
        .task {
            await agendaViewModel.refresh()
            // VoiceService.prepare() runs at HygurApp level — no need to
            // re-trigger here, and doing so on a per-ChatView .task
            // caused races (see HygurApp's voiceService comment).
        }
        .onReceive(NotificationCenter.default.publisher(for: .focusChatInput)) { note in
            // Triggered by the global summon hotkey and the menu bar Ask
            // Hygur action. When the notification carries a String we
            // prefill the input so the user can hit Send right away.
            if let prefill = note.object as? String, !prefill.isEmpty {
                viewModel.inputText = prefill
            }
        }
        .onReceive(NotificationCenter.default.publisher(for: .openAgendaSheet)) { _ in
            showingAgendaSheet = true
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
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            if !viewModel.pendingAttachments.isEmpty {
                pendingAttachmentsStrip
            }

            HStack(spacing: HygurSpacing.md) {
                Button {
                    presentAttachmentPicker()
                } label: {
                    Image(systemName: "paperclip")
                        .font(.title3)
                        .foregroundStyle(HygurColors.textSecondary)
                }
                .buttonStyle(.plain)
                .disabled(viewModel.isStreaming)
                .help("Attach an image or audio file")

                MicButton(
                    voiceService: voiceService,
                    inputText: $viewModel.inputText,
                    error: $viewModel.error,
                    isStreaming: viewModel.isStreaming
                )

                MarkdownEditorView(
                    text: $viewModel.inputText,
                    prompt: "Message... (Shift+Return for newline)",
                    showToolbar: false,
                    bordered: false,
                    minHeight: 36,
                    insets: NSSize(width: 8, height: 8),
                    font: .systemFont(ofSize: 13),
                    onSubmit: {
                        Task { await viewModel.send() }
                    }
                )
                .frame(maxHeight: 120)
                .background(HygurColors.surface)
                .clipShape(RoundedRectangle(cornerRadius: HygurRadius.md))
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
                    .disabled(canSend == false)
                }
            }
        }
        .padding(HygurSpacing.lg)
        .onDrop(of: [.image, .audio, .fileURL], isTargeted: nil) { providers in
            ingestProviders(providers)
            return true
        }
        .onPasteCommand(of: [UTType.image.identifier]) { providers in
            ingestProviders(providers)
        }
    }

    /// Send is enabled when either typed text or a queued attachment is
    /// present; an image-only message is a valid turn (the model can still
    /// reason from "what is this?" with empty text).
    private var canSend: Bool {
        !viewModel.inputText.trimmingCharacters(in: .whitespaces).isEmpty
            || !viewModel.pendingAttachments.isEmpty
    }

    /// Adapts the agenda badge to whichever signals exist. We bias toward
    /// urgency when there are extracted actions (orange "Focus") and toward
    /// neutrality when only calendar events are present (accent "Today").
    private var agendaBadgeLabel: String {
        let actionCount = agendaViewModel.actions.count
        let eventCount = agendaViewModel.calendarEvents.count
        if actionCount > 0 && eventCount > 0 {
            return "Focus: \(actionCount) action\(actionCount > 1 ? "s" : "") · \(eventCount) event\(eventCount > 1 ? "s" : "")"
        }
        if actionCount > 0 {
            return "Focus: \(actionCount) action\(actionCount > 1 ? "s" : "")"
        }
        return "Today: \(eventCount) event\(eventCount > 1 ? "s" : "")"
    }

    private var agendaBadgeSymbol: String {
        agendaViewModel.actions.count > 0 ? "target" : "calendar"
    }

    private var agendaBadgeTint: Color {
        agendaViewModel.actions.count > 0 ? .orange : HygurColors.accent
    }

    /// Horizontal strip of thumbnails for queued attachments. Each thumb has a
    /// remove "×" overlay so the user can drop a wrong image without resending.
    private var pendingAttachmentsStrip: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: HygurSpacing.sm) {
                ForEach(Array(viewModel.pendingAttachments.enumerated()), id: \.offset) { index, attachment in
                    pendingAttachmentThumb(attachment, index: index)
                }
            }
            .padding(.horizontal, HygurSpacing.xs)
        }
    }

    @ViewBuilder
    private func pendingAttachmentThumb(_ attachment: Attachment, index: Int) -> some View {
        ZStack(alignment: .topTrailing) {
            Group {
                switch attachment {
                case .image(let data, _):
                    if let nsImage = NSImage(data: data) {
                        Image(nsImage: nsImage)
                            .resizable()
                            .scaledToFill()
                            .frame(width: 64, height: 64)
                            .clipShape(RoundedRectangle(cornerRadius: HygurRadius.sm))
                    } else {
                        pendingPlaceholder(icon: "photo")
                    }
                case .audio:
                    pendingPlaceholder(icon: "waveform")
                case .document:
                    pendingPlaceholder(icon: "doc.text")
                }
            }

            Button {
                viewModel.removePendingAttachment(at: index)
            } label: {
                Image(systemName: "xmark.circle.fill")
                    .font(.caption)
                    .foregroundStyle(.white)
                    .background(Circle().fill(Color.black.opacity(0.6)))
            }
            .buttonStyle(.plain)
            .padding(2)
            .help("Remove")
        }
    }

    private func pendingPlaceholder(icon: String) -> some View {
        RoundedRectangle(cornerRadius: HygurRadius.sm)
            .fill(HygurColors.surface)
            .frame(width: 64, height: 64)
            .overlay(
                Image(systemName: icon)
                    .font(.title3)
                    .foregroundStyle(HygurColors.textSecondary)
            )
    }

    /// Open NSOpenPanel filtered to images and audio, then queue each file
    /// as the appropriate Attachment kind. Errors from disk read are
    /// surfaced via the chat error banner so the user is never silently
    /// stuck.
    private func presentAttachmentPicker() {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = true
        panel.canChooseDirectories = false
        panel.canChooseFiles = true
        panel.allowedContentTypes = [.image, .audio]
        guard panel.runModal() == .OK else { return }
        for url in panel.urls {
            ingestFileURL(url)
        }
    }

    /// Drain NSItemProviders from drop/paste. Image providers re-encode to
    /// PNG via NSBitmapImageRep for predictable wire format; audio (and
    /// generic file URLs) are routed through `ingestFileURL` which reads
    /// raw bytes off disk.
    private func ingestProviders(_ providers: [NSItemProvider]) {
        for provider in providers {
            // Try in-memory image first (catches pasted screenshots that
            // never had a file URL).
            if provider.canLoadObject(ofClass: NSImage.self),
               !provider.hasItemConformingToTypeIdentifier(UTType.audio.identifier),
               !provider.hasItemConformingToTypeIdentifier(UTType.fileURL.identifier) {
                provider.loadObject(ofClass: NSImage.self) { object, _ in
                    guard let nsImage = object as? NSImage,
                          let png = pngData(from: nsImage) else { return }
                    Task { @MainActor in
                        viewModel.addImage(data: png, mimeType: "image/png")
                    }
                }
                continue
            }
            // Fall back to file URL — works for both image and audio drops.
            provider.loadItem(forTypeIdentifier: UTType.fileURL.identifier) { item, _ in
                let url: URL?
                if let direct = item as? URL { url = direct }
                else if let data = item as? Data { url = URL(dataRepresentation: data, relativeTo: nil) }
                else { url = nil }
                guard let url else { return }
                Task { @MainActor in
                    ingestFileURL(url)
                }
            }
        }
    }

    /// Resolve a file URL to either an image or audio attachment based on
    /// its declared UTType. Unknown types are reported as an error. Audio
    /// duration is loaded asynchronously and folded into the attachment
    /// once known — the file is queued immediately so the UI stays
    /// responsive, then updated when duration resolves.
    private func ingestFileURL(_ url: URL) {
        guard let type = UTType(filenameExtension: url.pathExtension) else {
            viewModel.error = "Unsupported file type: \(url.lastPathComponent)"
            return
        }
        do {
            let data = try Data(contentsOf: url)
            if type.conforms(to: .image) {
                viewModel.addImage(data: data, mimeType: imageMime(for: type))
            } else if type.conforms(to: .audio) {
                let format = audioFormat(for: type, fileExtension: url.pathExtension)
                viewModel.addAudio(data: data, format: format, duration: nil)
                let queuedIndex = viewModel.pendingAttachments.count - 1
                Task {
                    if let duration = await audioDuration(for: url) {
                        viewModel.updatePendingAttachmentDuration(at: queuedIndex, to: duration)
                    }
                }
            } else {
                viewModel.error = "\(url.lastPathComponent) isn't an image or audio file."
            }
        } catch {
            viewModel.error = "Could not read \(url.lastPathComponent): \(error.localizedDescription)"
        }
    }

    private func imageMime(for type: UTType) -> String {
        switch type {
        case .png: return "image/png"
        case .jpeg: return "image/jpeg"
        case .gif: return "image/gif"
        case .webP: return "image/webp"
        case .heic: return "image/heic"
        case .tiff: return "image/tiff"
        default: return type.preferredMIMEType ?? "image/png"
        }
    }

    /// Map an audio UTType to the short tag the OpenAI multimodal spec
    /// expects ("wav", "mp3", "m4a", …). Falls back to the file extension
    /// when the UTType match is too generic.
    private func audioFormat(for type: UTType, fileExtension: String) -> String {
        if type == .wav { return "wav" }
        if type == .mp3 { return "mp3" }
        if type == .mpeg4Audio { return "m4a" }
        if type == .aiff { return "aiff" }
        let ext = fileExtension.lowercased()
        if !ext.isEmpty { return ext }
        return "wav"
    }

    private func audioDuration(for url: URL) async -> TimeInterval? {
        let asset = AVURLAsset(url: url)
        guard let cm = try? await asset.load(.duration),
              cm.isValid, !cm.isIndefinite, cm.timescale != 0 else { return nil }
        let seconds = CMTimeGetSeconds(cm)
        return seconds.isFinite && seconds > 0 ? seconds : nil
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

    // MARK: - TTS

    /// Toggle reading a finished assistant message aloud. A second click on
    /// the same message stops playback; clicking a different message
    /// interrupts the current one and starts the new one.
    private func toggleSpeak(_ message: Message) {
        if manuallySpokenMessageId == message.id {
            speechService.stop()
            manuallySpokenMessageId = nil
        } else {
            speechService.speak(stripMarkdown(message.content))
            manuallySpokenMessageId = message.id
        }
    }

    /// During streaming, send only the new tail of the active assistant
    /// message to the synthesizer so playback starts mid-answer. Markdown
    /// stripping happens lazily on each delta — fine for typical answers
    /// because the deltas are small.
    private func feedAutoSpeakIfNeeded() {
        guard autoSpeakResponses,
              viewModel.isStreaming,
              let last = viewModel.messages.last,
              last.role == .assistant else { return }
        let consumed = spokenLengths[last.id] ?? 0
        let total = last.content.count
        guard total > consumed else { return }
        let startIdx = last.content.index(last.content.startIndex, offsetBy: consumed)
        let delta = String(last.content[startIdx...])
        speechService.feed(stripMarkdown(delta))
        spokenLengths[last.id] = total
    }

    /// Lightly strip markdown so the synthesizer doesn't pronounce
    /// asterisks, backticks, or pipe-table noise. Cheap regex-style replace
    /// — full markdown parsing would be overkill for TTS.
    private func stripMarkdown(_ text: String) -> String {
        var s = text
        s = s.replacingOccurrences(of: "**", with: "")
        s = s.replacingOccurrences(of: "__", with: "")
        s = s.replacingOccurrences(of: "`", with: "")
        s = s.replacingOccurrences(of: "#", with: "")
        return s
    }
}

/// Click-to-toggle mic button. First click starts recording, second click
/// stops. Standard pattern (Whisper, Otter, ChatGPT iOS) — push-to-talk
/// turned out to interfere with SwiftUI layout next to the
/// `MarkdownEditorView`, and click-to-toggle is just as ergonomic.
///
/// Extracted as its own `View` so observation of `voiceService.isRecording`
/// only re-evaluates this subtree, not the whole chat layout.
private struct MicButton: View {
    let voiceService: VoiceService
    @Binding var inputText: String
    @Binding var error: String?
    let isStreaming: Bool
    /// Snapshot of `inputText` when recording starts so the live transcript
    /// appends instead of wiping any text the user already typed.
    @State private var voiceBaseline: String = ""

    var body: some View {
        let isOn = voiceService.isRecording
        let unavailable = !voiceService.isOnDeviceAvailable
        return Button {
            if isOn {
                voiceService.stop()
            } else {
                guard !isStreaming else { return }
                voiceBaseline = inputText
                Task { await voiceService.start() }
            }
        } label: {
            Image(systemName: isOn ? "mic.fill" : "mic")
                .font(.title3)
                .foregroundStyle(
                    isOn
                        ? Color.red
                        : (unavailable ? HygurColors.textTertiary : HygurColors.textSecondary)
                )
                .frame(width: 24, height: 24)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(isStreaming || unavailable)
        .help(
            unavailable
                ? "On-device speech recognition isn't installed for any of your preferred languages. Open System Settings → Keyboard → Dictation to install one."
                : (isOn ? "Click to stop recording" : "Click to record")
        )
        .onChange(of: voiceService.transcript) { _, newValue in
            guard voiceService.isRecording else { return }
            let glue = voiceBaseline.isEmpty || newValue.isEmpty ? "" : " "
            inputText = voiceBaseline + glue + newValue
        }
        .onChange(of: voiceService.error) { _, newError in
            if let newError {
                error = newError
                voiceService.error = nil
            }
        }
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
    var isSpeaking: Bool = false
    var onSpeak: (() -> Void)? = nil

    @State private var isHovered: Bool = false

    var body: some View {
        HStack {
            if message.role == .user { Spacer(minLength: 60) }

            VStack(alignment: message.role == .user ? .trailing : .leading, spacing: 4) {
                if let atts = message.attachments, !atts.isEmpty {
                    attachmentsPreview(atts)
                }
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
                            onRegenerate: onRegenerate,
                            isSpeaking: isSpeaking,
                            onSpeak: onSpeak
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

    // MARK: - Attachments Preview

    @ViewBuilder
    private func attachmentsPreview(_ attachments: [Attachment]) -> some View {
        let alignment: HorizontalAlignment = message.role == .user ? .trailing : .leading
        VStack(alignment: alignment, spacing: HygurSpacing.xs) {
            ForEach(Array(attachments.enumerated()), id: \.offset) { _, att in
                attachmentChip(att)
            }
        }
    }

    @ViewBuilder
    private func attachmentChip(_ attachment: Attachment) -> some View {
        switch attachment {
        case .image(let data, _):
            if let nsImage = NSImage(data: data) {
                Image(nsImage: nsImage)
                    .resizable()
                    .scaledToFill()
                    .frame(maxWidth: 120, maxHeight: 120)
                    .clipShape(RoundedRectangle(cornerRadius: HygurRadius.md))
            } else {
                attachmentLabel(icon: "photo", text: "Image")
            }
        case .audio(_, let format, let duration):
            let label = duration.map { String(format: "Audio · %.0fs", $0) } ?? "Audio (.\(format))"
            attachmentLabel(icon: "waveform", text: label)
        case .document(let contentId, let title):
            attachmentLabel(icon: "doc.text", text: title ?? contentId)
        }
    }

    private func attachmentLabel(icon: String, text: String) -> some View {
        HStack(spacing: HygurSpacing.xs) {
            Image(systemName: icon)
                .font(.caption)
                .foregroundStyle(HygurColors.textSecondary)
            Text(text)
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textSecondary)
                .lineLimit(1)
                .truncationMode(.middle)
        }
        .padding(.horizontal, HygurSpacing.sm)
        .padding(.vertical, HygurSpacing.xs)
        .background(HygurColors.surface)
        .clipShape(Capsule())
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

/// Re-encode an NSImage as PNG for transport. Returns nil when the image
/// has no bitmap representation we can write — caller should treat that as
/// "skip this drop", not as an error worth surfacing.
private func pngData(from image: NSImage) -> Data? {
    guard let tiff = image.tiffRepresentation,
          let rep = NSBitmapImageRep(data: tiff) else { return nil }
    return rep.representation(using: .png, properties: [:])
}

#Preview {
    ChatView(viewModel: ChatViewModel())
}
