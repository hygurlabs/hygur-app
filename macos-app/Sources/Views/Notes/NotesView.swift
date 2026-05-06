import SwiftUI

// MARK: - Notes View

/// Main view for displaying and managing notes.
struct NotesView: View {
    @StateObject private var viewModel = NotesViewModel()
    @Binding var showingNewNote: Bool
    @State private var searchText = ""
    @State private var selectedNote: Note?
    @State private var errorMessage: String?

    var body: some View {
        VStack(spacing: 0) {
            // Header
            FeatureHeader(title: "Notes", count: viewModel.notes.count) {
                IconButton(systemImage: "arrow.clockwise", label: "Refresh") {
                    Task { await viewModel.loadNotes() }
                }
                IconButton(systemImage: "plus", label: "New Note") {
                    showingNewNote = true
                }
            }

            Divider()

            // Content
            if viewModel.isLoading {
                LoadingIndicator(style: .large)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if viewModel.filteredNotes(searchText: searchText).isEmpty {
                emptyState
            } else {
                noteList
            }
        }
        .searchable(text: $searchText, prompt: "Search notes...")
        .task {
            await viewModel.loadNotes()
        }
        .onChange(of: viewModel.showError) { _, isShowing in
            if isShowing {
                errorMessage = viewModel.errorMessage
                viewModel.showError = false
            }
        }
        .errorBannerOverlay($errorMessage)
        .onChange(of: showingNewNote) { _, newValue in
            if !newValue {
                // Refresh notes when sheet closes
                Task {
                    await viewModel.loadNotes()
                }
            }
        }
    }

    // MARK: - Empty State

    private var emptyState: some View {
        Group {
            if searchText.isEmpty {
                EmptyStateView(
                    icon: "note.text",
                    title: "No notes yet",
                    subtitle: "Create your first note",
                    action: ("New Note", { showingNewNote = true })
                )
            } else {
                EmptyStateView(
                    icon: "magnifyingglass",
                    title: "No notes matching \"\(searchText)\""
                )
            }
        }
    }

    // MARK: - Note List

    private var noteList: some View {
        List {
            ForEach(viewModel.filteredNotes(searchText: searchText)) { note in
                NoteRow(note: note, viewModel: viewModel)
            }
        }
        .listStyle(.inset)
    }
}

// MARK: - Note Row

/// Single note row in the list.
struct NoteRow: View {
    let note: Note
    @ObservedObject var viewModel: NotesViewModel
    @State private var showingDeleteConfirmation = false
    @State private var showingEditSheet = false
    @State private var exportError: String?

    var body: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            // Title
            Text(note.title)
                .font(HygurTypography.headline)

            // Content preview
            Text(note.content)
                .font(HygurTypography.subheadline)
                .foregroundStyle(HygurColors.textSecondary)
                .lineLimit(2)

            // Tags and metadata
            HStack(spacing: HygurSpacing.sm) {
                // Tags
                if !note.tags.isEmpty {
                    ForEach(note.tags.prefix(3)) { tag in
                        TagPillView(tag: tag)
                    }
                    if note.tags.count > 3 {
                        Text("+\(note.tags.count - 3)")
                            .font(HygurTypography.caption)
                            .foregroundStyle(HygurColors.textSecondary)
                    }
                }

                Spacer()

                // Date
                Text(formattedDate(note.updatedAt))
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textTertiary)
            }
        }
        .padding(.vertical, HygurSpacing.xs)
        .contentShape(Rectangle())
        .onTapGesture(count: 2) {
            showingEditSheet = true
        }
        .contextMenu {
            contextMenuItems
        }
        .confirmationDialog(
            "Delete Note",
            isPresented: $showingDeleteConfirmation,
            titleVisibility: .visible
        ) {
            Button("Delete", role: .destructive) {
                Task {
                    await viewModel.deleteNote(note)
                }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Are you sure you want to delete \"\(note.title)\"? This action cannot be undone.")
        }
        .sheet(isPresented: $showingEditSheet) {
            EditNoteView(note: note) { updatedNote in
                viewModel.updateNoteInList(updatedNote)
            }
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

    // MARK: - Context Menu

    @ViewBuilder
    private var contextMenuItems: some View {
        Button {
            showingEditSheet = true
        } label: {
            Label("Edit", systemImage: "pencil")
        }

        Button {
            exportNote()
        } label: {
            Label("Export to Markdown…", systemImage: "square.and.arrow.up")
        }

        Button {
            exportNoteAsPDF()
        } label: {
            Label("Export to PDF…", systemImage: "doc.richtext")
        }

        Divider()

        Button(role: .destructive) {
            showingDeleteConfirmation = true
        } label: {
            Label("Delete", systemImage: "trash")
        }
    }

    private func exportNote() {
        do {
            try MarkdownExportService.exportNote(note)
        } catch MarkdownExportService.ExportError.userCancelled {
            // Silent — the user dismissed the save panel intentionally.
        } catch {
            exportError = error.localizedDescription
        }
    }

    private func exportNoteAsPDF() {
        do {
            try PDFExportService.exportNote(note)
        } catch PDFExportService.ExportError.userCancelled {
            // Silent — the user dismissed the save panel intentionally.
        } catch {
            exportError = error.localizedDescription
        }
    }

    // MARK: - Helpers

    private func formattedDate(_ date: Date) -> String {
        let formatter = RelativeDateTimeFormatter()
        formatter.unitsStyle = .abbreviated
        return formatter.localizedString(for: date, relativeTo: Date())
    }
}

// MARK: - View Model

@MainActor
class NotesViewModel: ObservableObject {
    @Published var notes: [Note] = []
    @Published var isLoading = false
    @Published var showError = false
    @Published var errorMessage = ""

    private let sidecar = SidecarService.fromSettings()

    // MARK: - Filtering

    func filteredNotes(searchText: String) -> [Note] {
        let sorted = notes.sorted { $0.updatedAt > $1.updatedAt }
        if searchText.isEmpty {
            return sorted
        }
        return sorted.filter {
            $0.title.localizedCaseInsensitiveContains(searchText) ||
            $0.content.localizedCaseInsensitiveContains(searchText) ||
            $0.tags.contains { $0.name.localizedCaseInsensitiveContains(searchText) }
        }
    }

    // MARK: - Actions

    func loadNotes() async {
        isLoading = true
        defer { isLoading = false }

        do {
            notes = try await sidecar.listNotes()
            SpotlightIndexer.reindexAllNotes(notes)
        } catch {
            showError(error)
        }
    }

    func deleteNote(_ note: Note) async {
        do {
            try await sidecar.deleteNote(id: note.id)
            notes.removeAll { $0.id == note.id }
            SpotlightIndexer.removeNote(id: note.id)
        } catch {
            showError(error)
        }
    }

    func updateNoteInList(_ updatedNote: Note) {
        if let index = notes.firstIndex(where: { $0.id == updatedNote.id }) {
            notes[index] = updatedNote
        }
        SpotlightIndexer.index(note: updatedNote)
    }

    // MARK: - Error Handling

    private func showError(_ error: Error) {
        errorMessage = error.localizedDescription
        showError = true
        print("NotesViewModel error: \(error)")
    }
}

// MARK: - Previews

#Preview("Notes View") {
    @Previewable @State var showingNewNote = false
    NotesView(showingNewNote: $showingNewNote)
        .frame(width: 400, height: 500)
}
