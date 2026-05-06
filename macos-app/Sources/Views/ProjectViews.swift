import SwiftUI

// MARK: - Project List View

/// Main view for displaying and managing projects
struct ProjectListView: View {
    @StateObject private var viewModel = ProjectListViewModel()
    @State private var showingNewProject = false
    @State private var selectedProject: Project?
    @State private var errorMessage: String?

    var body: some View {
        VStack(spacing: 0) {
            // Header
            FeatureHeader(title: "Projects", count: viewModel.projects.count) {
                IconButton(systemImage: "plus", label: "New Project") {
                    showingNewProject = true
                }
            }

            Divider()

            // Content
            if viewModel.isLoading {
                LoadingIndicator(style: .large)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if viewModel.projects.isEmpty {
                EmptyStateView(
                    icon: "folder",
                    title: "No projects yet",
                    subtitle: "Create a project to organize your knowledge items",
                    action: ("Create Project", { showingNewProject = true })
                )
            } else {
                projectList
            }
        }
        .sheet(isPresented: $showingNewProject) {
            NewProjectSheet(viewModel: viewModel)
        }
        .task {
            await viewModel.loadProjects()
        }
        .onChange(of: viewModel.showError) { _, isShowing in
            if isShowing {
                errorMessage = viewModel.errorMessage
                viewModel.showError = false
            }
        }
        .errorBannerOverlay($errorMessage)
    }

    // MARK: - Project List

    private var projectList: some View {
        List {
            // Active projects
            Section {
                ForEach(viewModel.activeProjects) { project in
                    ProjectRow(project: project, viewModel: viewModel)
                }
            }

            // Archived projects (if any)
            if !viewModel.archivedProjects.isEmpty {
                Section("Archived") {
                    ForEach(viewModel.archivedProjects) { project in
                        ProjectRow(project: project, viewModel: viewModel)
                    }
                }
            }
        }
        .listStyle(.inset)
    }
}

// MARK: - Project Row

/// Single project row in the list
struct ProjectRow: View {
    let project: Project
    @ObservedObject var viewModel: ProjectListViewModel
    @State private var showingEditSheet = false
    @State private var showingDeleteConfirmation = false
    @State private var showingDetailSheet = false

    var body: some View {
        HStack(spacing: HygurSpacing.md) {
            // Project icon
            Image(systemName: project.archived ? "archivebox" : "folder.fill")
                .font(.title2)
                .foregroundColor(project.archived ? HygurColors.textSecondary : HygurColors.accent)
                .frame(width: 32)

            // Project info
            VStack(alignment: .leading, spacing: HygurSpacing.xxs) {
                Text(project.name)
                    .font(HygurTypography.headline)
                    .foregroundStyle(project.archived ? HygurColors.textSecondary : HygurColors.textPrimary)

                if let description = project.description, !description.isEmpty {
                    Text(description)
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                        .lineLimit(1)
                }

                HStack(spacing: HygurSpacing.sm) {
                    Label("\(project.itemCount)", systemImage: "doc.text")
                        .font(HygurTypography.captionMono)
                        .foregroundStyle(HygurColors.textTertiary)

                    if !project.tags.isEmpty {
                        ForEach(project.tags.prefix(3), id: \.self) { tag in
                            Text(tag)
                                .font(HygurTypography.captionMono)
                                .padding(.horizontal, HygurSpacing.xs)
                                .padding(.vertical, HygurSpacing.xxs)
                                .background(HygurColors.accent.opacity(0.15))
                                .foregroundStyle(HygurColors.accent)
                                .cornerRadius(HygurRadius.xs)
                        }
                        if project.tags.count > 3 {
                            Text("+\(project.tags.count - 3)")
                                .font(HygurTypography.captionMono)
                                .foregroundStyle(HygurColors.textSecondary)
                        }
                    }

                    Text(formattedDate(project.updatedAt))
                        .font(HygurTypography.captionMono)
                        .foregroundStyle(HygurColors.textTertiary)
                }
            }

            Spacer()

            // Item count badge
            Text("\(project.itemCount)")
                .font(HygurTypography.caption)
                .fontWeight(.medium)
                .padding(.horizontal, HygurSpacing.sm)
                .padding(.vertical, HygurSpacing.xs)
                .background(Color.secondary.opacity(0.15))
                .clipShape(Capsule())
        }
        .padding(.vertical, HygurSpacing.xs)
        .opacity(project.archived ? 0.6 : 1)
        .contentShape(Rectangle())
        .onTapGesture(count: 2) {
            showingDetailSheet = true
        }
        .contextMenu {
            contextMenuItems
        }
        .sheet(isPresented: $showingEditSheet) {
            EditProjectSheet(project: project, viewModel: viewModel)
        }
        .sheet(isPresented: $showingDetailSheet) {
            ProjectDetailView(project: project)
        }
        .confirmationDialog(
            "Delete Project",
            isPresented: $showingDeleteConfirmation,
            titleVisibility: .visible
        ) {
            Button("Delete", role: .destructive) {
                Task {
                    await viewModel.deleteProject(project)
                }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Are you sure you want to delete \"\(project.name)\"? This action cannot be undone.")
        }
    }

    // MARK: - Context Menu

    @ViewBuilder
    private var contextMenuItems: some View {
        Button {
            showingDetailSheet = true
        } label: {
            Label("View Documents", systemImage: "doc.text.magnifyingglass")
        }

        Button {
            showingEditSheet = true
        } label: {
            Label("Edit", systemImage: "pencil")
        }

        Button {
            Task {
                await viewModel.toggleArchive(project)
            }
        } label: {
            Label(
                project.archived ? "Unarchive" : "Archive",
                systemImage: project.archived ? "tray.and.arrow.up" : "archivebox"
            )
        }

        Divider()

        Button(role: .destructive) {
            showingDeleteConfirmation = true
        } label: {
            Label("Delete", systemImage: "trash")
        }
    }

    // MARK: - Helpers

    private func formattedDate(_ date: Date) -> String {
        let formatter = RelativeDateTimeFormatter()
        formatter.unitsStyle = .abbreviated
        return formatter.localizedString(for: date, relativeTo: Date())
    }
}

// MARK: - New Project Sheet

/// Sheet for creating a new project
struct NewProjectSheet: View {
    @ObservedObject var viewModel: ProjectListViewModel
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var description = ""
    @State private var tagsText = ""
    @State private var isCreating = false

    var body: some View {
        VStack(spacing: HygurSpacing.xl) {
            Text("New Project")
                .font(HygurTypography.headline)

            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                Text("Name")
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textSecondary)
                TextField("Project Name", text: $name)
                    .textFieldStyle(.roundedBorder)
            }

            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                Text("Description")
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textSecondary)
                TextField("Description (optional)", text: $description)
                    .textFieldStyle(.roundedBorder)
            }

            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                Text("Tags")
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textSecondary)
                TextField("Tags (comma-separated)", text: $tagsText)
                    .textFieldStyle(.roundedBorder)
            }

            HStack {
                Button("Cancel", role: .cancel) { dismiss() }
                    .keyboardShortcut(.escape, modifiers: [])

                Spacer()

                Button("Create") {
                    createProject()
                }
                .buttonStyle(.borderedProminent)
                .disabled(name.isEmpty || isCreating)
                .keyboardShortcut(.return, modifiers: .command)
            }
        }
        .padding(HygurSpacing.lg)
        .frame(minWidth: 520, idealWidth: 560)
    }

    private func createProject() {
        guard !name.isEmpty else { return }

        let tags = tagsText
            .split(separator: ",")
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }

        isCreating = true
        Task {
            await viewModel.createProject(
                name: name,
                description: description.isEmpty ? nil : description,
                tags: tags.isEmpty ? nil : tags
            )
            dismiss()
        }
    }
}

// MARK: - Edit Project Sheet

/// Sheet for editing an existing project
struct EditProjectSheet: View {
    let project: Project
    @ObservedObject var viewModel: ProjectListViewModel
    @Environment(\.dismiss) private var dismiss
    @State private var name: String
    @State private var description: String
    @State private var tagsText: String
    @State private var isSaving = false

    init(project: Project, viewModel: ProjectListViewModel) {
        self.project = project
        self.viewModel = viewModel
        _name = State(initialValue: project.name)
        _description = State(initialValue: project.description ?? "")
        _tagsText = State(initialValue: project.tags.joined(separator: ", "))
    }

    var body: some View {
        VStack(spacing: HygurSpacing.xl) {
            Text("Edit Project")
                .font(HygurTypography.headline)

            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                Text("Name")
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textSecondary)
                TextField("Project Name", text: $name)
                    .textFieldStyle(.roundedBorder)
            }

            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                Text("Description")
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textSecondary)
                TextField("Description (optional)", text: $description)
                    .textFieldStyle(.roundedBorder)
            }

            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                Text("Tags")
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textSecondary)
                TextField("Tags (comma-separated)", text: $tagsText)
                    .textFieldStyle(.roundedBorder)
            }

            HStack {
                Button("Cancel", role: .cancel) { dismiss() }
                    .keyboardShortcut(.escape, modifiers: [])

                Spacer()

                Button("Save") {
                    saveProject()
                }
                .buttonStyle(.borderedProminent)
                .disabled(name.isEmpty || isSaving)
                .keyboardShortcut(.return, modifiers: .command)
            }
        }
        .padding(HygurSpacing.lg)
        .frame(minWidth: 520, idealWidth: 560)
    }

    private func saveProject() {
        guard !name.isEmpty else { return }

        let tags = tagsText
            .split(separator: ",")
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }

        isSaving = true
        Task {
            await viewModel.updateProject(
                project,
                name: name,
                description: description.isEmpty ? nil : description,
                tags: tags
            )
            dismiss()
        }
    }
}


// MARK: - Project Detail View

/// Sheet showing all documents in a project with full details
struct ProjectDetailView: View {
    let project: Project
    @Environment(\.dismiss) private var dismiss
    @Environment(EventStreamService.self) private var events
    @Environment(ChatSessionManager.self) private var chatSessions
    @State private var items: [ProjectItem] = []
    @State private var isLoading = false
    @State private var errorMessage: String?
    @State private var selectedItem: ProjectItem?
    @State private var showingNoteEditor = false
    @State private var editingNoteId: String?
    @State private var showingNewNote = false
    @State private var briefRunning = false
    /// Wall-clock at which the last project brief was requested. Cleared
    /// when a `brief` SSE event with `receivedAt >= briefRequestedAt`
    /// arrives or after the 90 s watchdog. See MenubarPanelView for the
    /// same pattern.
    @State private var briefRequestedAt: Date?

    private let sidecar = SidecarService.fromSettings()

    /// Chat sessions associated with this project — pulled from the live
    /// session manager so updates (renames, new sessions) reflect without a
    /// reload. We hide empty/untitled sessions: a session that was never
    /// sent doesn't tell the user anything useful.
    private var linkedSessions: [ChatSession] {
        chatSessions.sessions(forProject: project.id)
            .sorted { $0.updatedAt > $1.updatedAt }
    }

    var body: some View {
        HSplitView {
            // Main content: documents list + linked chats
            VStack(spacing: 0) {
                header

                Divider()

                if isLoading {
                    LoadingIndicator(style: .large)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else if let error = errorMessage, items.isEmpty, linkedSessions.isEmpty {
                    errorState(error)
                } else {
                    documentsAndChatsBody
                }
            }
            .frame(minWidth: 520)
            .errorBannerOverlay($errorMessage)

            // Metadata panel
            if let item = selectedItem {
                ProjectItemMetadataPanel(item: item)
                    .frame(width: 280)
            }
        }
        .frame(minWidth: 880, idealWidth: 1000, minHeight: 620, idealHeight: 720)
        .task {
            await loadItems()
        }
        .onChange(of: events.recentEvents.count) { _, _ in
            handleEventStreamUpdate()
        }
        .sheet(isPresented: $showingNoteEditor) {
            if let noteId = editingNoteId {
                EditNoteFromProjectView(noteId: noteId) {
                    Task { await loadItems() }
                }
            }
        }
        .sheet(isPresented: $showingNewNote) {
            // CreateNoteView creates the note via the sidecar; we then
            // refresh items so the new note appears in this view without a
            // round-trip through the parent list.
            CreateNoteView { newNote in
                Task {
                    if newNote.projectId != project.id {
                        // The user explicitly picked a different project in
                        // the modal — don't override their choice. We just
                        // refresh in case items changed elsewhere.
                    }
                    await loadItems()
                }
            }
        }
    }

    // MARK: - Combined body (documents + chats)

    @ViewBuilder
    private var documentsAndChatsBody: some View {
        VSplitView {
            VStack(alignment: .leading, spacing: 0) {
                sectionHeader("Documents", count: items.count, systemImage: "doc.text")
                if items.isEmpty {
                    EmptyStateView(
                        icon: "doc.text",
                        title: "No documents",
                        subtitle: "This project has no linked documents yet"
                    )
                } else {
                    itemList
                }
            }
            .frame(minHeight: 200)

            VStack(alignment: .leading, spacing: 0) {
                sectionHeader("Linked chats", count: linkedSessions.count, systemImage: "bubble.left.and.bubble.right")
                if linkedSessions.isEmpty {
                    Text("No chat sessions are tagged with this project yet.")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textTertiary)
                        .padding(HygurSpacing.lg)
                        .frame(maxWidth: .infinity, alignment: .leading)
                } else {
                    chatList
                }
            }
            .frame(minHeight: 140, idealHeight: 220)
        }
    }

    private func sectionHeader(_ title: String, count: Int, systemImage: String) -> some View {
        HStack(spacing: HygurSpacing.sm) {
            Image(systemName: systemImage)
                .foregroundStyle(HygurColors.textSecondary)
            Text(title)
                .font(HygurTypography.subheadline)
                .fontWeight(.semibold)
            Text("\(count)")
                .font(HygurTypography.captionMono)
                .foregroundStyle(HygurColors.textTertiary)
            Spacer()
        }
        .padding(.horizontal, HygurSpacing.lg)
        .padding(.vertical, HygurSpacing.sm)
        .background(HygurColors.surface.opacity(0.5))
    }

    private var chatList: some View {
        List {
            ForEach(linkedSessions) { session in
                LinkedChatSessionRow(session: session) {
                    // Open the chat in the main window: switch to the
                    // chat session, dismiss this sheet so the user lands
                    // on the conversation immediately.
                    NotificationCenter.default.post(
                        name: .navigateToSection,
                        object: "chat"
                    )
                    NotificationCenter.default.post(
                        name: .openChatSession,
                        object: session.id.uuidString
                    )
                    dismiss()
                }
            }
        }
        .listStyle(.inset)
    }

    // MARK: - Header

    private var header: some View {
        HStack(spacing: HygurSpacing.lg) {
            Image(systemName: project.archived ? "archivebox.fill" : "folder.fill")
                .font(.title)
                .foregroundColor(project.archived ? HygurColors.textSecondary : HygurColors.accent)

            VStack(alignment: .leading, spacing: HygurSpacing.xs) {
                Text(project.name)
                    .font(HygurTypography.title3)
                    .fontWeight(.semibold)

                if let description = project.description, !description.isEmpty {
                    Text(description)
                        .font(HygurTypography.subheadline)
                        .foregroundStyle(HygurColors.textSecondary)
                }

                HStack(spacing: HygurSpacing.sm) {
                    Label("\(items.count) items", systemImage: "doc.text")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textTertiary)

                    if !project.tags.isEmpty {
                        ForEach(project.tags.prefix(3), id: \.self) { tag in
                            Text(tag)
                                .font(HygurTypography.caption)
                                .padding(.horizontal, HygurSpacing.xs)
                                .padding(.vertical, HygurSpacing.xxs)
                                .background(HygurColors.accent.opacity(0.15))
                                .foregroundStyle(HygurColors.accent)
                                .cornerRadius(HygurRadius.xs)
                        }
                        if project.tags.count > 3 {
                            Text("+\(project.tags.count - 3)")
                                .font(HygurTypography.caption)
                                .foregroundStyle(HygurColors.textSecondary)
                        }
                    }
                }
            }

            Spacer()

            // Quick-create a note that auto-binds to this project. Solves
            // the user complaint that the project modal didn't expose a
            // "create note for this project" action — they had to leave the
            // sheet, open the new-note flow, and re-pick the project.
            Button {
                showingNewNote = true
            } label: {
                Label("Add Note", systemImage: "square.and.pencil")
            }
            .help("Create a note attached to this project")

            // Meeting-prep helper: run a project-scoped brief that pulls
            // every item linked to the project through the LLM and
            // surfaces the digest as a `brief` event in Activity. Lookback
            // is 14 days by default — wide enough to cover a sprint or a
            // real-estate negotiation, narrow enough to skip stale context.
            Button {
                triggerProjectBrief()
            } label: {
                if briefRunning {
                    Label {
                        Text("Briefing…")
                    } icon: {
                        ProgressView().controlSize(.small)
                    }
                } else {
                    Label("Brief this project", systemImage: "doc.text.below.ecg")
                }
            }
            .disabled(briefRunning || items.isEmpty)
            .help("Generate an LLM digest of this project for meeting prep")

            Button("Done") {
                dismiss()
            }
            .keyboardShortcut(.escape)
        }
        .padding(HygurSpacing.lg)
    }

    private func triggerProjectBrief() {
        guard !briefRunning else { return }
        briefRunning = true
        let requestedAt = Date()
        briefRequestedAt = requestedAt
        Task {
            do {
                _ = try await sidecar.runBrief(projectId: project.id, lookbackHours: 14 * 24)
                // Open Activity so the user sees the brief land.
                NotificationCenter.default.post(name: .navigateToSection, object: "activity")
                // Watchdog: project briefs run on the same LLM, expect 10-30 s.
                try? await Task.sleep(nanoseconds: 90 * 1_000_000_000)
                if briefRequestedAt == requestedAt {
                    briefRunning = false
                    briefRequestedAt = nil
                }
            } catch SidecarError.serviceUnavailable {
                errorMessage = "Brief unavailable — set daily_brief.enabled=true in config.yaml"
                briefRunning = false
                briefRequestedAt = nil
            } catch {
                errorMessage = "Brief failed: \(error.localizedDescription)"
                briefRunning = false
                briefRequestedAt = nil
            }
        }
    }

    /// Mirrors `MenubarPanelView.handleEventStreamUpdate` — clears the
    /// running flag as soon as a fresh `brief` event lands on the SSE
    /// stream, short-circuiting the 90 s watchdog.
    private func handleEventStreamUpdate() {
        guard let requestedAt = briefRequestedAt else { return }
        let landed = events.recentEvents.contains { evt in
            evt.type == "brief" && evt.receivedAt >= requestedAt
        }
        if landed {
            briefRunning = false
            briefRequestedAt = nil
        }
    }

    // MARK: - Error State

    private func errorState(_ message: String) -> some View {
        VStack(spacing: HygurSpacing.md) {
            Image(systemName: "exclamationmark.triangle")
                .font(.system(size: 40))
                .foregroundStyle(HygurColors.warning)
            Text("Failed to load documents")
                .font(HygurTypography.headline)
            Text(message)
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textSecondary)
            Button("Retry") {
                Task { await loadItems() }
            }
            .buttonStyle(.bordered)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    // MARK: - Item List

    private var itemList: some View {
        List(selection: $selectedItem) {
            ForEach(items) { item in
                ProjectItemRow(
                    item: item,
                    isSelected: selectedItem?.id == item.id,
                    onDoubleClick: {
                        if item.sourceType == "note" {
                            editingNoteId = item.id
                            showingNoteEditor = true
                        }
                    }
                )
                .tag(item)
            }
        }
        .listStyle(.inset)
    }

    // MARK: - Actions

    private func loadItems() async {
        isLoading = true
        errorMessage = nil
        defer { isLoading = false }

        do {
            items = try await sidecar.listProjectItems(projectId: project.id)
        } catch {
            errorMessage = error.localizedDescription
            print("ProjectDetailView error: \(error)")
        }
    }
}

// MARK: - Project Item Metadata Panel

/// Side panel showing metadata for a selected item
struct ProjectItemMetadataPanel: View {
    let item: ProjectItem

    var body: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.lg) {
            // Header
            HStack(spacing: HygurSpacing.md) {
                Image(systemName: item.sourceTypeIcon)
                    .font(.title)
                    .foregroundColor(HygurColors.accent)

                VStack(alignment: .leading, spacing: HygurSpacing.xxs) {
                    Text(item.title)
                        .font(HygurTypography.headline)
                        .lineLimit(2)
                    Text(item.sourceType.capitalized)
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                }
            }

            Divider()

            // Metadata
            VStack(alignment: .leading, spacing: HygurSpacing.md) {
                metadataRow("Type", item.sourceType)

                if let path = item.sourcePath {
                    metadataRow("Path", path)
                }

                metadataRow("Created", formatDate(item.createdAt))
                metadataRow("Updated", formatDate(item.updatedAt))
            }

            Spacer()

            // Actions
            if let path = item.sourcePath {
                Button {
                    openInFinder(path)
                } label: {
                    Label("Show in Finder", systemImage: "folder")
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.bordered)
            }
        }
        .padding(HygurSpacing.lg)
        .background(HygurColors.background)
    }

    private func metadataRow(_ label: String, _ value: String) -> some View {
        VStack(alignment: .leading, spacing: HygurSpacing.xxs) {
            Text(label)
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textSecondary)
            Text(value)
                .font(HygurTypography.callout)
                .lineLimit(3)
        }
    }

    private func formatDate(_ date: Date) -> String {
        let formatter = DateFormatter()
        formatter.dateStyle = .medium
        formatter.timeStyle = .short
        return formatter.string(from: date)
    }

    private func openInFinder(_ path: String) {
        let url = URL(fileURLWithPath: path)
        NSWorkspace.shared.activateFileViewerSelecting([url])
    }
}

// MARK: - Edit Note From Project View

/// Sheet for editing a note from within the project detail view
struct EditNoteFromProjectView: View {
    let noteId: String
    let onSave: () -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var note: Note?
    @State private var title = ""
    @State private var content = ""
    @State private var isLoading = true
    @State private var isSaving = false
    @State private var errorMessage: String?

    private let sidecar = SidecarService.fromSettings()

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            if isLoading {
                LoadingIndicator(style: .large)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                editor
            }
        }
        .frame(minWidth: 500, minHeight: 400)
        .task {
            await loadNote()
        }
        .errorBannerOverlay($errorMessage)
    }

    private var header: some View {
        HStack {
            Text("Edit Note")
                .font(HygurTypography.headline)
            Spacer()
            Button("Cancel", role: .cancel) { dismiss() }
                .keyboardShortcut(.escape, modifiers: [])
            Button("Save") { saveNote() }
                .buttonStyle(.borderedProminent)
                .disabled(title.isEmpty || isSaving)
                .keyboardShortcut(.return, modifiers: .command)
        }
        .padding(HygurSpacing.lg)
    }

    private var editor: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.md) {
            TextField("Title", text: $title)
                .textFieldStyle(.roundedBorder)
                .font(HygurTypography.title3)

            TextEditor(text: $content)
                .font(HygurTypography.body)
                .frame(maxHeight: .infinity)
        }
        .padding(HygurSpacing.lg)
    }

    private func loadNote() async {
        isLoading = true
        defer { isLoading = false }

        do {
            note = try await sidecar.getNote(id: noteId)
            title = note?.title ?? ""
            content = note?.content ?? ""
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func saveNote() {
        isSaving = true
        Task {
            do {
                _ = try await sidecar.updateNote(id: noteId, title: title, content: content)
                onSave()
                dismiss()
            } catch {
                errorMessage = error.localizedDescription
            }
            isSaving = false
        }
    }
}

// MARK: - Project Item Row

/// Single document row in the project detail view
struct ProjectItemRow: View {
    let item: ProjectItem
    var isSelected: Bool = false
    var onDoubleClick: (() -> Void)?

    var body: some View {
        HStack(spacing: HygurSpacing.md) {
            Image(systemName: item.sourceTypeIcon)
                .font(.title3)
                .foregroundColor(HygurColors.accent)
                .frame(width: 28)

            VStack(alignment: .leading, spacing: HygurSpacing.xxs) {
                Text(item.title)
                    .font(HygurTypography.body)
                    .lineLimit(1)

                HStack(spacing: HygurSpacing.sm) {
                    Text(item.sourceType)
                        .font(HygurTypography.caption)
                        .padding(.horizontal, HygurSpacing.xs)
                        .padding(.vertical, HygurSpacing.xxs)
                        .background(Color.secondary.opacity(0.15))
                        .cornerRadius(HygurRadius.xs)

                    if let path = item.sourcePath {
                        Text(path)
                            .font(HygurTypography.caption)
                            .foregroundStyle(HygurColors.textTertiary)
                            .lineLimit(1)
                    }

                    Spacer()

                    Text(formattedDate(item.updatedAt))
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textTertiary)
                }
            }
        }
        .padding(.vertical, HygurSpacing.xs)
        .contentShape(Rectangle())
        .onTapGesture(count: 2) {
            onDoubleClick?()
        }
    }

    private func formattedDate(_ date: Date) -> String {
        let formatter = RelativeDateTimeFormatter()
        formatter.unitsStyle = .abbreviated
        return formatter.localizedString(for: date, relativeTo: Date())
    }
}

// MARK: - View Model

/// View model for the project list
@MainActor
class ProjectListViewModel: ObservableObject {
    @Published var projects: [Project] = []
    @Published var isLoading = false
    @Published var showError = false
    @Published var errorMessage = ""

    private let sidecar = SidecarService.fromSettings()

    // MARK: - Computed Properties

    var activeProjects: [Project] {
        projects.filter { !$0.archived }
    }

    var archivedProjects: [Project] {
        projects.filter { $0.archived }
    }

    // MARK: - Actions

    func loadProjects() async {
        isLoading = true
        defer { isLoading = false }

        do {
            projects = try await sidecar.listProjects()
        } catch {
            showError(error)
        }
    }

    func createProject(name: String, description: String?, tags: [String]? = nil) async {
        do {
            let project = try await sidecar.createProject(name: name, description: description, tags: tags)
            projects.insert(project, at: 0)
        } catch {
            showError(error)
        }
    }

    func updateProject(_ project: Project, name: String, description: String?, tags: [String]? = nil) async {
        do {
            let updated = try await sidecar.updateProject(
                id: project.id,
                name: name,
                description: description,
                tags: tags
            )
            if let index = projects.firstIndex(where: { $0.id == project.id }) {
                projects[index] = updated
            }
        } catch {
            showError(error)
        }
    }

    func deleteProject(_ project: Project) async {
        do {
            try await sidecar.deleteProject(id: project.id)
            projects.removeAll { $0.id == project.id }
        } catch {
            showError(error)
        }
    }

    func toggleArchive(_ project: Project) async {
        do {
            let updated = try await sidecar.toggleProjectArchive(
                id: project.id,
                archived: !project.archived
            )
            if let index = projects.firstIndex(where: { $0.id == project.id }) {
                projects[index] = updated
            }
        } catch {
            showError(error)
        }
    }

    // MARK: - Error Handling

    private func showError(_ error: Error) {
        errorMessage = error.localizedDescription
        showError = true
        print("ProjectListViewModel error: \(error)")
    }
}

// MARK: - Chat session row

/// Compact row used by ProjectDetailView's "Linked chats" section.
/// Tap (or double-click) to navigate the main window to the conversation.
private struct LinkedChatSessionRow: View {
    let session: ChatSession
    let onOpen: () -> Void

    @State private var isHovered = false

    var body: some View {
        HStack(spacing: HygurSpacing.md) {
            Image(systemName: session.isPinned ? "pin.fill" : "bubble.left")
                .foregroundStyle(session.isPinned ? HygurColors.accent : HygurColors.textSecondary)
                .frame(width: 18)

            VStack(alignment: .leading, spacing: 2) {
                Text(session.displayTitle)
                    .font(HygurTypography.body)
                    .lineLimit(1)
                if let preview = session.lastMessagePreview {
                    Text(preview)
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textTertiary)
                        .lineLimit(1)
                }
            }

            Spacer()

            Text(formattedDate(session.updatedAt))
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textTertiary)
        }
        .padding(.vertical, HygurSpacing.xs)
        .padding(.horizontal, HygurSpacing.sm)
        .background(
            RoundedRectangle(cornerRadius: HygurRadius.sm)
                .fill(isHovered ? HygurColors.accent.opacity(0.08) : Color.clear)
        )
        .contentShape(Rectangle())
        .onHover { isHovered = $0 }
        .onTapGesture(count: 2, perform: onOpen)
    }

    private func formattedDate(_ date: Date) -> String {
        let formatter = RelativeDateTimeFormatter()
        formatter.unitsStyle = .abbreviated
        return formatter.localizedString(for: date, relativeTo: Date())
    }
}

// MARK: - Previews

#Preview("Project List") {
    ProjectListView()
        .frame(width: 400, height: 500)
}

#Preview("Empty State") {
    ProjectListView()
        .frame(width: 400, height: 500)
}
