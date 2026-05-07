import SwiftUI

/// Async wrapper around `EditNoteView` that fetches a note by id from the
/// sidecar. Used when a favorited note is selected from the sidebar — we
/// only have the id at that point, so we fetch the full note before handing
/// it to the existing edit flow.
struct NoteDetailLoader: View {
    let noteId: String

    @State private var note: Note?
    @State private var loadError: String?

    private let sidecar = SidecarService.fromSettings()

    var body: some View {
        Group {
            if let note {
                EditNoteView(note: note)
            } else if let loadError {
                EmptyDetailPlaceholder(
                    systemImage: "exclamationmark.triangle",
                    title: "Couldn't load note",
                    subtitle: loadError
                )
            } else {
                ProgressView()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background(.regularMaterial)
            }
        }
        .task(id: noteId) {
            await load()
        }
    }

    private func load() async {
        do {
            note = try await sidecar.getNote(id: noteId)
        } catch {
            loadError = error.localizedDescription
        }
    }
}
