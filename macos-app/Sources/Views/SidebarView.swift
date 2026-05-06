import SwiftUI

enum SidebarItem: Hashable {
    case newChat
    case chatSession(UUID)
    case knowledgeBase
    case notes
    case projects
    case tags
    case search
    case email
    case graph
    case connectors
    case activity
    case memories
    case marketplace
}

struct SidebarView: View {
    @Binding var selection: SidebarItem?
    @Binding var showingNewNote: Bool
    @State var sessionManager: ChatSessionManager

    @State private var searchText: String = ""

    // MARK: - Collapsible section state (persisted)

    @AppStorage("sidebar.chat.expanded")         private var chatExpanded         = true
    @AppStorage("sidebar.knowledge.expanded")    private var knowledgeExpanded    = true
    @AppStorage("sidebar.organization.expanded") private var orgExpanded          = true
    @AppStorage("sidebar.integrations.expanded") private var integrationsExpanded = true

    var body: some View {
        List(selection: $selection) {
            // MARK: Chat

            Section(isExpanded: $chatExpanded) {
                // Inline search — only when sessions exist
                if !sessionManager.sessions.isEmpty {
                    HStack(spacing: 6) {
                        Image(systemName: "magnifyingglass")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .accessibilityHidden(true)
                        TextField("Search chats…", text: $searchText)
                            .textFieldStyle(.plain)
                            .font(.callout)
                    }
                    .padding(.horizontal, 8)
                    .padding(.vertical, 5)
                    .background(.quaternary.opacity(0.6), in: RoundedRectangle(cornerRadius: 6))
                    .padding(.bottom, 2)
                    // Not tagged — must not be selectable as a SidebarItem
                    .listRowInsets(EdgeInsets(top: 2, leading: 8, bottom: 2, trailing: 8))
                }

                Button {
                    let session = sessionManager.createSession()
                    selection = .chatSession(session.id)
                } label: {
                    Label("New Chat", systemImage: "plus.bubble")
                }
                .buttonStyle(.plain)

                if !sessionManager.sessions.isEmpty {
                    chatSessionsList
                }
            } header: {
                sectionHeader(
                    "Chat",
                    count: sessionManager.sessions.isEmpty ? nil : sessionManager.sessions.count
                ) {
                    chatExpanded.toggle()
                }
            }

            // MARK: Knowledge

            Section(isExpanded: $knowledgeExpanded) {
                Label("Knowledge Base", systemImage: "doc.text.magnifyingglass")
                    .tag(SidebarItem.knowledgeBase)

                Label("Notes", systemImage: "note.text")
                    .tag(SidebarItem.notes)

                Label("Timeline", systemImage: "clock.arrow.circlepath")
                    .tag(SidebarItem.graph)

                Label("Search", systemImage: "magnifyingglass")
                    .tag(SidebarItem.search)

                Label("Memories", systemImage: "brain.head.profile")
                    .tag(SidebarItem.memories)

                Label("Activity", systemImage: "bell.badge")
                    .tag(SidebarItem.activity)
            } header: {
                sectionHeader("Knowledge") {
                    knowledgeExpanded.toggle()
                }
            }

            // MARK: Organization

            Section(isExpanded: $orgExpanded) {
                Label("Projects", systemImage: "folder")
                    .tag(SidebarItem.projects)

                Label("Tags", systemImage: "tag")
                    .tag(SidebarItem.tags)
            } header: {
                sectionHeader("Organization") {
                    orgExpanded.toggle()
                }
            }

            // MARK: Integrations
            // Email Threads removed — folded into ConnectorDetailView.
            // The `SidebarItem.email` enum case is kept for backward compatibility.

            Section(isExpanded: $integrationsExpanded) {
                Label("Connectors", systemImage: "puzzlepiece.extension")
                    .tag(SidebarItem.connectors)
                Label("Marketplace", systemImage: "bag")
                    .tag(SidebarItem.marketplace)
            } header: {
                sectionHeader("Integrations") {
                    integrationsExpanded.toggle()
                }
            }
        }
        .listStyle(.sidebar)
        .frame(minWidth: 220)
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Menu {
                    Button {
                        let session = sessionManager.createSession()
                        selection = .chatSession(session.id)
                    } label: {
                        Label("New Chat", systemImage: "bubble.left.and.bubble.right")
                    }

                    Button {
                        showingNewNote = true
                    } label: {
                        Label("New Note", systemImage: "note.text.badge.plus")
                    }
                } label: {
                    Image(systemName: "plus")
                    .accessibilityLabel("Create new item")
                }
                .help("Create new item")
            }
        }
    }

    // MARK: - Section Header

    @ViewBuilder
    private func sectionHeader(
        _ title: String,
        count: Int? = nil,
        onToggle: @escaping () -> Void
    ) -> some View {
        Button(action: onToggle) {
            HStack(spacing: 4) {
                Text(title)
                    .font(.caption)
                    .fontWeight(.semibold)
                    .tracking(0.6)
                    .foregroundStyle(HygurColors.textTertiary)

                if let count, count > 0 {
                    Text("\(count)")
                        .font(.caption2)
                        .fontWeight(.medium)
                        .foregroundStyle(HygurColors.textTertiary)
                        .padding(.horizontal, 5)
                        .padding(.vertical, 1)
                        .background(.quaternary, in: Capsule())
                }

                Spacer()
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }

    // MARK: - Chat Sessions List

    @ViewBuilder
    private var chatSessionsList: some View {
        let filteredSessions = searchText.isEmpty
            ? sessionManager.sessions
            : sessionManager.search(query: searchText)

        let pinned = filteredSessions.filter { $0.isPinned }
        if !pinned.isEmpty {
            ForEach(pinned) { session in
                ChatSessionRow(
                    session: session,
                    isSelected: isSelected(session),
                    onDelete: { sessionManager.deleteSession(session.id) },
                    onPin: { sessionManager.togglePin(for: session.id) },
                    onRename: { newTitle in sessionManager.updateTitle(newTitle, for: session.id) },
                    onUpdateProject: { projectId in sessionManager.updateProject(projectId, for: session.id) },
                    onUpdateTags: { tagIds in sessionManager.updateTags(tagIds, for: session.id) }
                )
                .tag(SidebarItem.chatSession(session.id))
            }
        }

        let recent = filteredSessions.filter { !$0.isPinned }
        if !recent.isEmpty {
            ForEach(recent) { session in
                ChatSessionRow(
                    session: session,
                    isSelected: isSelected(session),
                    onDelete: { sessionManager.deleteSession(session.id) },
                    onPin: { sessionManager.togglePin(for: session.id) },
                    onRename: { newTitle in sessionManager.updateTitle(newTitle, for: session.id) },
                    onUpdateProject: { projectId in sessionManager.updateProject(projectId, for: session.id) },
                    onUpdateTags: { tagIds in sessionManager.updateTags(tagIds, for: session.id) }
                )
                .tag(SidebarItem.chatSession(session.id))
            }
        }
    }

    private func isSelected(_ session: ChatSession) -> Bool {
        if case .chatSession(let id) = selection {
            return id == session.id
        }
        return false
    }
}

// MARK: - Chat Session Row

struct ChatSessionRow: View {
    let session: ChatSession
    let isSelected: Bool
    let onDelete: () -> Void
    let onPin: () -> Void
    let onRename: (String) -> Void
    var onUpdateProject: ((String?) -> Void)?
    var onUpdateTags: (([String]) -> Void)?

    @State private var isHovered = false
    @State private var isRenaming = false
    @State private var editedTitle: String = ""
    @State private var showingOrganizeSheet = false
    @State private var exportError: String?
    @FocusState private var isTitleFieldFocused: Bool

    var body: some View {
        HStack(spacing: 8) {
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 4) {
                    if session.isPinned {
                        Image(systemName: "pin.fill")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                            .accessibilityHidden(true)
                    }

                    if session.projectId != nil {
                        Image(systemName: "folder.fill")
                            .font(.caption2)
                            .foregroundStyle(.orange)
                            .accessibilityHidden(true)
                    }

                    if isRenaming {
                        TextField("Chat title", text: $editedTitle)
                            .textFieldStyle(.plain)
                            .font(.body)
                            .focused($isTitleFieldFocused)
                            .onSubmit {
                                commitRename()
                            }
                            .onExitCommand {
                                cancelRename()
                            }
                    } else {
                        Text(session.displayTitle)
                            .font(.body)
                            .lineLimit(1)
                    }
                }

                HStack(spacing: 4) {
                    if let preview = session.lastMessagePreview, !isRenaming {
                        Text(preview)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }

                    if !session.tagIds.isEmpty {
                        Spacer()
                        HStack(spacing: 2) {
                            Image(systemName: "tag.fill")
                                .font(.caption2)
                                .accessibilityHidden(true)
                            Text("\(session.tagIds.count)")
                                .font(.caption2)
                        }
                        .foregroundStyle(.purple)
                    }
                }
            }

            Spacer()

            if isHovered && !isRenaming {
                Text(session.updatedAt.formatted(.relative(presentation: .named)))
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
            }
        }
        .padding(.vertical, 2)
        .contentShape(Rectangle())
        .onHover { isHovered = $0 }
        .contextMenu {
            Button {
                startRenaming()
            } label: {
                Label("Rename", systemImage: "pencil")
            }

            Button {
                onPin()
            } label: {
                Label(session.isPinned ? "Unpin" : "Pin", systemImage: session.isPinned ? "pin.slash" : "pin")
            }

            Button {
                showingOrganizeSheet = true
            } label: {
                Label("Organize...", systemImage: "folder.badge.gearshape")
            }

            Button {
                exportSession()
            } label: {
                Label("Export to Markdown…", systemImage: "square.and.arrow.up")
            }
            .disabled(!session.hasMessages)

            Button {
                exportSessionAsPDF()
            } label: {
                Label("Export to PDF…", systemImage: "doc.richtext")
            }
            .disabled(!session.hasMessages)

            Divider()

            Button(role: .destructive) {
                onDelete()
            } label: {
                Label("Delete", systemImage: "trash")
            }
        }
        .sheet(isPresented: $showingOrganizeSheet) {
            ChatOrganizeSheet(
                session: session,
                onUpdateProject: onUpdateProject,
                onUpdateTags: onUpdateTags
            )
        }
        .alert("Export failed", isPresented: .init(
            get: { exportError != nil },
            set: { if !$0 { exportError = nil } }
        )) {
            Button("OK") { exportError = nil }
        } message: {
            Text(exportError ?? "")
        }
    }

    private func startRenaming() {
        editedTitle = session.title == "New Chat" ? "" : session.title
        isRenaming = true
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
            isTitleFieldFocused = true
        }
    }

    private func commitRename() {
        let trimmedTitle = editedTitle.trimmingCharacters(in: .whitespaces)
        if !trimmedTitle.isEmpty {
            onRename(trimmedTitle)
        }
        isRenaming = false
        isTitleFieldFocused = false
    }

    private func cancelRename() {
        isRenaming = false
        isTitleFieldFocused = false
        editedTitle = ""
    }

    private func exportSession() {
        do {
            try MarkdownExportService.exportChatSession(session)
        } catch MarkdownExportService.ExportError.userCancelled {
            // Silent — the user dismissed the save panel intentionally.
        } catch {
            exportError = error.localizedDescription
        }
    }

    private func exportSessionAsPDF() {
        do {
            try PDFExportService.exportChatSession(session)
        } catch PDFExportService.ExportError.userCancelled {
            // Silent — the user dismissed the save panel intentionally.
        } catch {
            exportError = error.localizedDescription
        }
    }
}

#Preview {
    @Previewable @State var selection: SidebarItem? = .newChat
    @Previewable @State var showingNewNote = false
    SidebarView(
        selection: $selection,
        showingNewNote: $showingNewNote,
        sessionManager: ChatSessionManager()
    )
}
