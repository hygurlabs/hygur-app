import SwiftUI

/// Async wrapper around `ProjectDetailView` that fetches a project by id from
/// the sidecar. Mirrors `NoteDetailLoader` — used by sidebar favorites.
struct ProjectDetailLoader: View {
    let projectId: String

    @State private var project: Project?
    @State private var loadError: String?

    private let sidecar = SidecarService.fromSettings()

    var body: some View {
        Group {
            if let project {
                ProjectDetailView(project: project)
            } else if let loadError {
                EmptyDetailPlaceholder(
                    systemImage: "exclamationmark.triangle",
                    title: "Couldn't load project",
                    subtitle: loadError
                )
            } else {
                ProgressView()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background(.regularMaterial)
            }
        }
        .task(id: projectId) {
            await load()
        }
    }

    private func load() async {
        do {
            project = try await sidecar.getProject(id: projectId)
        } catch {
            loadError = error.localizedDescription
        }
    }
}
