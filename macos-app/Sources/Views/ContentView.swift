import SwiftUI
import AppKit
import UniformTypeIdentifiers

struct ContentView: View {
    @AppStorage("lastSidebarSelection") private var lastSidebarSelectionRaw: String = "chat"
    /// Right-hand inspector visibility — Phase 3 introduces the toggle; the
    /// pane currently hosts a placeholder, real inspectors land in Phase 6.
    @AppStorage("hygur.properties.visible") private var propertiesVisible: Bool = false

    @State private var sidebarSelection: SidebarItem? = .newChat
    @State private var showingNewNote = false
    @State private var showingCommandPalette = false
    @State private var sessionManager = ChatSessionManager()
    @State private var chatViewModel = ChatViewModel()
    @Environment(InspectorSelection.self) private var inspector
    @Environment(EventStreamService.self) private var eventStream

    /// Gates the launch-time runtime offline banner. Two layers:
    /// - `runtimeBannerArmed` flips on after a short grace window so we
    ///   don't flash the banner during the normal SSE startup race.
    /// - `runtimeBannerDismissed` is a per-session opt-out so users who
    ///   know their runtime is intentionally offline aren't nagged.
    @AppStorage("onboarding.completed") private var onboardingCompleted: Bool = false
    @State private var runtimeBannerArmed: Bool = false
    @State private var runtimeBannerDismissed: Bool = false

    var body: some View {
        ZStack {
            VStack(spacing: 0) {
                if shouldShowRuntimeBanner {
                    RuntimeUnreachableBanner(
                        onConfigure: openSettings,
                        onDismiss: {
                            withAnimation(.easeInOut(duration: 0.2)) {
                                runtimeBannerDismissed = true
                            }
                        }
                    )
                    .transition(.move(edge: .top).combined(with: .opacity))
                }
                NavigationSplitView {
                    SidebarView(
                        selection: $sidebarSelection,
                        showingNewNote: $showingNewNote,
                        sessionManager: sessionManager
                    )
                } detail: {
                    ColumnRouter.main(
                        for: sidebarSelection,
                        chatViewModel: chatViewModel,
                        showingNewNote: $showingNewNote
                    )
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background(HygurColors.background)
                }
                .inspector(isPresented: $propertiesVisible) {
                    PropertiesPanel(selection: sidebarSelection)
                        .inspectorColumnWidth(min: 240, ideal: 280, max: 360)
                }
                .onChange(of: sidebarSelection) { _, newValue in
                    handleSelectionChange(newValue)
                }
                .toolbar {
                    ToolbarItem(placement: .principal) {
                        if isChatView {
                            ModelSelectorView()
                        }
                    }
                    // Flexible spacer + Properties toggle anchors the inspector
                    // toggle to the absolute trailing edge of the window toolbar.
                    ToolbarSpacer(.flexible, placement: .primaryAction)
                    ToolbarItem(placement: .primaryAction) {
                        Button {
                            propertiesVisible.toggle()
                        } label: {
                            Image(systemName: propertiesVisible ? "sidebar.right" : "sidebar.squares.right")
                        }
                        .help(propertiesVisible ? "Hide Properties" : "Show Properties")
                    }
                }
                .sheet(isPresented: $showingNewNote) {
                    CreateNoteView()
                }

                HygurStatusBar()
            }
            .frame(minWidth: 1000, minHeight: 700)

            // Command palette as an in-window overlay rather than a modal
            // sheet. Sheets on macOS don't dismiss on outside-click, which is
            // jarring for a Spotlight-style interface; the overlay approach
            // also lets Cmd+K toggle the palette without fighting AppKit's
            // sheet lifecycle.
            if showingCommandPalette {
                CommandPaletteView(
                    sessionManager: sessionManager,
                    onExecute: { action in
                        showingCommandPalette = false
                        handleCommandAction(action)
                    },
                    onDismiss: { showingCommandPalette = false }
                )
                .transition(.opacity.combined(with: .scale(scale: 0.98)))
                .zIndex(1000)
            }
        }
        .animation(.easeOut(duration: 0.15), value: showingCommandPalette)
        // ChatSessionManager is also exposed via the SwiftUI environment so
        // detail views (ProjectDetailView, etc.) can read project-linked
        // sessions without us threading the manager through every initializer.
        .environment(sessionManager)
        .onReceive(NotificationCenter.default.publisher(for: .showNewNoteSheet)) { _ in
            showingNewNote = true
        }
        .onReceive(NotificationCenter.default.publisher(for: .showCommandPalette)) { _ in
            // Toggle: pressing Cmd+K twice closes the palette instead of
            // re-presenting an already-visible sheet (which macOS just ignores).
            showingCommandPalette.toggle()
        }
        .onReceive(NotificationCenter.default.publisher(for: .navigateToSection)) { notification in
            guard let sectionKey = notification.object as? String else { return }
            navigateToSection(sectionKey)
        }
        .onReceive(NotificationCenter.default.publisher(for: .openChatSession)) { notification in
            guard let raw = notification.object as? String,
                  let uuid = UUID(uuidString: raw) else { return }
            chatViewModel.bind(to: sessionManager, sessionId: uuid)
            sidebarSelection = .chatSession(uuid)
        }
        .onAppear {
            restoreLastSelection()
        }
        .task {
            // Grace window before we trust `lmStudioStatus == .down` — at
            // launch the SSE stream and the first /health probe race the
            // app appearing on screen, and we'd otherwise flash the banner
            // for ~1 s before the seed result lands.
            try? await Task.sleep(nanoseconds: 4_000_000_000)
            runtimeBannerArmed = true
        }
        // Re-arm the banner if the runtime flips back down later in the
        // session — the user may have dismissed it earlier, but a fresh
        // failure should be re-surfaceable. Only resets on transitions to
        // .down; flips to .up just hide the banner via shouldShowRuntimeBanner.
        .onChange(of: eventStream.lmStudioStatus) { _, newStatus in
            if newStatus == .down {
                runtimeBannerDismissed = false
            }
        }
        // Single-click in any list view (Notes, KB, Mail) writes to
        // InspectorSelection — auto-open the Properties pane so the user
        // gets immediate visible feedback rather than wondering whether the
        // click registered.
        .onChange(of: inspector.current) { _, newValue in
            if newValue != nil && !propertiesVisible {
                propertiesVisible = true
            }
        }
    }

    // MARK: - Selection Handling

    private func handleSelectionChange(_ newSelection: SidebarItem?) {
        switch newSelection {
        case .newChat:
            let session = sessionManager.createSession()
            chatViewModel.bind(to: sessionManager, sessionId: session.id)
            sidebarSelection = .chatSession(session.id)

        case .chatSession(let sessionId):
            chatViewModel.bind(to: sessionManager, sessionId: sessionId)

        case .note:
            // Favorited note clicked — keep the deep-link selection so
            // ColumnRouter can show the editor in the detail column. Just
            // unbind chat state.
            chatViewModel.unbind()

        case .project:
            // Same idea as `.note` — keep the deep-link, ColumnRouter handles routing.
            chatViewModel.unbind()

        default:
            chatViewModel.unbind()
            if let key = sidebarKey(for: newSelection) {
                lastSidebarSelectionRaw = key
            }
        }
    }

    private func restoreLastSelection() {
        // Only restore non-chat selections. Chat state starts fresh each launch.
        guard lastSidebarSelectionRaw != "chat" else { return }
        if let item = sidebarItem(for: lastSidebarSelectionRaw) {
            sidebarSelection = item
            handleSelectionChange(item)
        }
    }

    private func navigateToSection(_ key: String) {
        if let item = sidebarItem(for: key) {
            sidebarSelection = item
        }
    }

    // MARK: - Command Palette Dispatch

    /// Executes a `CommandAction` raised by the command palette. The palette
    /// stays UI-only — all state transitions (sidebar selection, sheet flags,
    /// session creation, network calls) happen here so there's a single
    /// source of truth.
    private func handleCommandAction(_ action: CommandAction) {
        switch action {
        case .navigate(let item):
            sidebarSelection = item
        case .openChatSession(let id):
            sidebarSelection = .chatSession(id)
        case .createNewChat:
            sidebarSelection = .newChat
        case .createNote:
            showingNewNote = true
        case .syncAllMail:
            Task { await syncAllMailAccounts() }
        case .indexFile:
            Task { await indexFileFromPalette() }
        }
    }

    /// Best-effort sync of every connected mail account. Silent on success;
    /// surfaces a console log on failure so the user isn't blocked by a
    /// transient connector issue.
    private func syncAllMailAccounts() async {
        let sidecar = SidecarService.fromSettings()
        do {
            let accounts = try await sidecar.listMailAccounts()
            for account in accounts where account.isConnected {
                _ = try? await sidecar.triggerMailSync(
                    accountId: account.accountId,
                    full: false,
                    async: true
                )
            }
        } catch {
            print("[CommandPalette] sync mail failed: \(error.localizedDescription)")
        }
    }

    /// Opens an NSOpenPanel and ingests the chosen file. Routed via the
    /// palette so the user can ingest without leaving their current view.
    @MainActor
    private func indexFileFromPalette() async {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = false
        panel.canChooseDirectories = false
        panel.canChooseFiles = true
        panel.title = "Index a file"
        panel.allowedContentTypes = Self.indexFilePanelTypes
        guard panel.runModal() == .OK, let url = panel.url else { return }

        do {
            _ = try await SidecarService.fromSettings().ingestFile(path: url.path)
        } catch {
            print("[CommandPalette] ingest failed: \(error.localizedDescription)")
        }
    }

    /// Content types accepted by the "Index File" open panel.
    private static var indexFilePanelTypes: [UTType] {
        var types: [UTType] = [
            .plainText, .pdf,
            UTType(filenameExtension: "md") ?? .plainText,
            UTType(filenameExtension: "markdown") ?? .plainText,
            UTType(filenameExtension: "docx") ?? .data,
            UTType(filenameExtension: "doc") ?? .data,
            .html,
            // Image types
            .png, .jpeg,
            UTType(filenameExtension: "heic") ?? .image,
            UTType(filenameExtension: "webp") ?? .image
        ]
        // Audio types — use filenameExtension fallback for maximum compatibility
        let audioExts = ["mp3", "m4a", "wav", "ogg"]
        for ext in audioExts {
            types.append(UTType(filenameExtension: ext) ?? .audio)
        }
        return types
    }

    // MARK: - Helpers

    private var isChatView: Bool {
        switch sidebarSelection {
        case .newChat, .chatSession, .none:
            return true
        default:
            return false
        }
    }

    private func sidebarKey(for item: SidebarItem?) -> String? {
        switch item {
        case .knowledgeBase: return "knowledgeBase"
        case .notes:         return "notes"
        case .search:        return "search"
        case .projects:      return "projects"
        case .tags:          return "tags"
        case .graph:         return "graph"
        case .connectors:    return "connectors"
        case .marketplace:   return "marketplace"
        case .email:         return "email"
        case .activity:      return "activity"
        case .memories:      return "memories"
        case .note(let id):    return "note:\(id)"
        case .project(let id): return "project:\(id)"
        default:             return nil
        }
    }

    private func sidebarItem(for key: String) -> SidebarItem? {
        if let id = key.hygur_stripping(prefix: "note:") {
            return .note(id)
        }
        if let id = key.hygur_stripping(prefix: "project:") {
            return .project(id)
        }
        switch key {
        case "chat":          return .newChat
        case "knowledgeBase": return .knowledgeBase
        case "notes":         return .notes
        case "search":        return .search
        case "projects":      return .projects
        case "tags":          return .tags
        case "graph":         return .graph
        case "connectors":    return .connectors
        case "marketplace":   return .marketplace
        case "email":         return .email
        case "activity":      return .activity
        case "memories":      return .memories
        default:              return nil
        }
    }

    // MARK: - Runtime banner

    /// All conditions that must be true for the launch-time banner to show.
    /// We deliberately don't surface this during onboarding (the user is
    /// already in setup) or before the grace window (false-flash on launch).
    private var shouldShowRuntimeBanner: Bool {
        guard onboardingCompleted else { return false }
        guard runtimeBannerArmed else { return false }
        guard !runtimeBannerDismissed else { return false }
        return eventStream.lmStudioStatus == .down
    }

    /// Opens the Settings window. macOS 14+ ships `showSettings(_:)` as the
    /// preferred selector — `showPreferences` is the old name and was
    /// soft-deprecated. Falls through silently if neither selector exists.
    private func openSettings() {
        if NSApp.responds(to: Selector(("showSettingsWindow:"))) {
            NSApp.sendAction(Selector(("showSettingsWindow:")), to: nil, from: nil)
        } else {
            NSApp.sendAction(Selector(("showPreferencesWindow:")), to: nil, from: nil)
        }
    }
}

private extension String {
    /// Returns the suffix after `prefix`, or nil if the prefix isn't matched.
    /// Used to round-trip sidebar deep-links like `note:abc-123` from
    /// `@AppStorage` without juggling Codable for an enum with associated values.
    func hygur_stripping(prefix: String) -> String? {
        guard hasPrefix(prefix) else { return nil }
        return String(dropFirst(prefix.count))
    }
}

#Preview {
    @Previewable @State var sessionManager = ChatSessionManager()
    ContentView()
}
