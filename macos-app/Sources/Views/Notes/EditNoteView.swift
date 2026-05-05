import SwiftUI

// MARK: - Edit Note View

/// Sheet/modal for editing existing notes.
struct EditNoteView: View {
    @Environment(\.dismiss) private var dismiss
    @StateObject private var viewModel: EditNoteViewModel
    var onNoteUpdated: ((Note) -> Void)?
    @State private var errorMessage: String?

    init(note: Note, onNoteUpdated: ((Note) -> Void)? = nil) {
        _viewModel = StateObject(wrappedValue: EditNoteViewModel(note: note))
        self.onNoteUpdated = onNoteUpdated
    }

    var body: some View {
        VStack(spacing: 0) {
            // Header
            header

            Divider()

            // Form content
            ScrollView {
                VStack(alignment: .leading, spacing: HygurSpacing.xl) {
                    // Title field
                    titleField

                    // Content editor
                    contentEditor

                    // Project picker
                    projectPicker

                    // Tags picker
                    tagsPicker
                }
                .padding(HygurSpacing.lg)
            }

            Divider()

            // Actions
            actionBar
        }
        .frame(minWidth: 500, idealWidth: 600, minHeight: 500, idealHeight: 600)
        .task {
            await viewModel.loadData()
        }
        .onChange(of: viewModel.showError) { _, isShowing in
            if isShowing {
                errorMessage = viewModel.errorMessage
                viewModel.showError = false
            }
        }
        .errorBannerOverlay($errorMessage)
    }

    // MARK: - Header

    private var header: some View {
        HStack {
            Text("Edit Note")
                .font(HygurTypography.headline)
            Spacer()
            IconButton(systemImage: "xmark.circle.fill", label: "Close") {
                dismiss()
            }
        }
        .padding(HygurSpacing.lg)
    }

    // MARK: - Title Field

    private var titleField: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            Text("Title")
                .font(HygurTypography.subheadline)
                .foregroundStyle(HygurColors.textSecondary)
            TextField("Note title", text: $viewModel.title)
                .textFieldStyle(.roundedBorder)
                .font(HygurTypography.title3)
        }
    }

    // MARK: - Content Editor

    private var contentEditor: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            HStack {
                Text("Content")
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textSecondary)
                Spacer()
                Text("Markdown supported")
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textTertiary)
            }
            TextEditor(text: $viewModel.content)
                .font(HygurTypography.body)
                .frame(minHeight: 200)
                .padding(HygurSpacing.sm)
                .background(HygurColors.surface)
                .cornerRadius(HygurRadius.md)
                .overlay(
                    RoundedRectangle(cornerRadius: HygurRadius.md)
                        .strokeBorder(Color.secondary.opacity(0.2), lineWidth: 1)
                )
        }
    }

    // MARK: - Project Picker

    private var projectPicker: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            Text("Project (optional)")
                .font(HygurTypography.subheadline)
                .foregroundStyle(HygurColors.textSecondary)

            if viewModel.isLoadingProjects {
                HStack(spacing: HygurSpacing.sm) {
                    LoadingIndicator(style: .small)
                    Text("Loading projects...")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                }
            } else {
                Picker("Project", selection: $viewModel.selectedProjectId) {
                    Text("None")
                        .tag(String?.none)
                    ForEach(viewModel.projects) { project in
                        Text(project.name)
                            .tag(String?.some(project.id))
                    }
                }
                .labelsHidden()
                .pickerStyle(.menu)
            }
        }
    }

    // MARK: - Tags Picker

    private var tagsPicker: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            HStack {
                Text("Tags (optional)")
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textSecondary)
                Spacer()
                if !viewModel.selectedTags.isEmpty {
                    Button("Clear all") {
                        viewModel.selectedTags.removeAll()
                    }
                    .font(HygurTypography.caption)
                    .buttonStyle(.plain)
                    .foregroundStyle(HygurColors.textSecondary)
                }
            }

            if viewModel.isLoadingTags {
                HStack(spacing: HygurSpacing.sm) {
                    LoadingIndicator(style: .small)
                    Text("Loading tags...")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                }
            } else if viewModel.availableTags.isEmpty {
                Text("No tags available. Create tags in the Tags section.")
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textTertiary)
            } else {
                // Selected tags
                if !viewModel.selectedTags.isEmpty {
                    FlowLayout(spacing: HygurSpacing.sm) {
                        ForEach(viewModel.selectedTags) { tag in
                            TagPillView(tag: tag, showRemoveButton: true) {
                                viewModel.selectedTags.removeAll { $0.id == tag.id }
                            }
                        }
                    }
                    .padding(.bottom, HygurSpacing.sm)
                }

                // Available tags
                FlowLayout(spacing: HygurSpacing.sm) {
                    ForEach(viewModel.unselectedTags) { tag in
                        SelectableTagPillView(tag: tag, isSelected: false) {
                            viewModel.selectedTags.append(tag)
                        }
                    }
                }
            }
        }
    }

    // MARK: - Action Bar

    private var actionBar: some View {
        HStack {
            Button("Cancel", role: .cancel) { dismiss() }
                .keyboardShortcut(.escape, modifiers: [])

            Spacer()

            if viewModel.hasChanges {
                Text("Unsaved changes")
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.warning)
            }

            Button("Save") {
                saveNote()
            }
            .buttonStyle(.borderedProminent)
            .disabled(!viewModel.isValid || viewModel.isSaving || !viewModel.hasChanges)
            .keyboardShortcut(.return, modifiers: .command)
        }
        .padding(HygurSpacing.lg)
    }

    // MARK: - Actions

    private func saveNote() {
        Task {
            if let note = await viewModel.saveNote() {
                onNoteUpdated?(note)
                dismiss()
            }
        }
    }
}

// MARK: - View Model

@MainActor
class EditNoteViewModel: ObservableObject {
    private let originalNote: Note

    @Published var title: String
    @Published var content: String
    @Published var selectedProjectId: String?
    @Published var selectedTags: [Tag]

    @Published var projects: [Project] = []
    @Published var availableTags: [Tag] = []

    @Published var isLoadingProjects = false
    @Published var isLoadingTags = false
    @Published var isSaving = false

    @Published var showError = false
    @Published var errorMessage = ""

    private let sidecar = SidecarService.fromSettings()

    init(note: Note) {
        self.originalNote = note
        self.title = note.title
        self.content = note.content
        self.selectedProjectId = note.projectId
        self.selectedTags = note.tags
    }

    var isValid: Bool {
        !title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty &&
        !content.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    var hasChanges: Bool {
        title != originalNote.title ||
        content != originalNote.content ||
        selectedProjectId != originalNote.projectId ||
        Set(selectedTags.map { $0.id }) != Set(originalNote.tags.map { $0.id })
    }

    var unselectedTags: [Tag] {
        availableTags.filter { tag in
            !selectedTags.contains { $0.id == tag.id }
        }
    }

    func loadData() async {
        await withTaskGroup(of: Void.self) { group in
            group.addTask { await self.loadProjects() }
            group.addTask { await self.loadTags() }
        }
    }

    func loadProjects() async {
        isLoadingProjects = true
        defer { isLoadingProjects = false }

        do {
            projects = try await sidecar.listProjects()
                .filter { !$0.archived }
        } catch {
            print("Failed to load projects: \(error)")
        }
    }

    func loadTags() async {
        isLoadingTags = true
        defer { isLoadingTags = false }

        do {
            availableTags = try await sidecar.listTags()
        } catch {
            print("Failed to load tags: \(error)")
        }
    }

    func saveNote() async -> Note? {
        guard isValid && hasChanges else { return nil }

        isSaving = true
        defer { isSaving = false }

        do {
            let tagIds = selectedTags.isEmpty ? nil : selectedTags.map { $0.id }
            let updatedNote = try await sidecar.updateNote(
                id: originalNote.id,
                title: title.trimmingCharacters(in: .whitespacesAndNewlines),
                content: content,
                projectId: selectedProjectId,
                tagIds: tagIds
            )
            return updatedNote
        } catch {
            showError(error)
            return nil
        }
    }

    private func showError(_ error: Error) {
        errorMessage = error.localizedDescription
        showError = true
        print("EditNoteViewModel error: \(error)")
    }
}

// MARK: - Previews

#Preview("Edit Note") {
    EditNoteView(note: Note(
        id: "note:preview",
        title: "Sample Note",
        content: "This is the content of the note.\n\nIt supports **Markdown**.",
        tags: []
    ))
}
