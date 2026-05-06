import SwiftUI
import AppKit
import UniformTypeIdentifiers

struct ContentView: View {
    @AppStorage("lastSidebarSelection") private var lastSidebarSelectionRaw: String = "chat"

    @State private var sidebarSelection: SidebarItem? = .newChat
    @State private var showingNewNote = false
    @State private var showingCommandPalette = false
    @State private var sessionManager = ChatSessionManager()
    @State private var chatViewModel = ChatViewModel()

    var body: some View {
        ZStack {
            NavigationSplitView {
                SidebarView(
                    selection: $sidebarSelection,
                    showingNewNote: $showingNewNote,
                    sessionManager: sessionManager
                )
            } detail: {
                detailView
            }
            .onChange(of: sidebarSelection) { _, newValue in
                handleSelectionChange(newValue)
            }
            .frame(minWidth: 800, minHeight: 600)
            .toolbar {
                ToolbarItem(placement: .principal) {
                    if isChatView {
                        ModelSelectorView()
                    }
                }
            }
            .sheet(isPresented: $showingNewNote) {
                CreateNoteView()
            }

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
    }

    // MARK: - Detail View

    @ViewBuilder
    private var detailView: some View {
        switch sidebarSelection {
        case .newChat:
            ChatView(viewModel: chatViewModel)
        case .chatSession(let sessionId):
            ChatView(viewModel: chatViewModel)
                .id(sessionId)
        case .knowledgeBase:
            KnowledgeBaseView()
        case .notes:
            NotesView(showingNewNote: $showingNewNote)
        case .search:
            SearchView()
        case .projects:
            ProjectListView()
        case .tags:
            TagsView()
        case .email:
            EmailThreadsView()
        case .graph:
            MemoryTimelineView()
        case .connectors:
            ConnectorsView()
        case .marketplace:
            ConnectorMarketplaceView()
        case .activity:
            ActivityView()
        case .memories:
            MemoriesView()
        case .none:
            ChatView(viewModel: chatViewModel)
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
        default:             return nil
        }
    }

    private func sidebarItem(for key: String) -> SidebarItem? {
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
}

#Preview {
    @Previewable @State var sessionManager = ChatSessionManager()
    ContentView()
}
