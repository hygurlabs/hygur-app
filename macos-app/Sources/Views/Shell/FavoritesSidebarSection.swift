import SwiftUI

/// Sidebar section listing favorited notes and projects. Names are fetched
/// lazily from the sidecar; the underlying ID set lives in `FavoritesStore`
/// (persisted to `UserDefaults`). When the sidecar is unreachable the section
/// renders the IDs gracefully without blocking the rest of the sidebar.
struct FavoritesSidebarSection: View {
    @Environment(FavoritesStore.self) private var favorites
    @AppStorage("sidebar.favorites.expanded") private var expanded: Bool = true

    @State private var noteLookup: [String: String] = [:]   // id → title
    @State private var projectLookup: [String: String] = [:]  // id → name

    private static let visibleCap = 12

    var body: some View {
        let noteIds = Array(favorites.favoriteNoteIds).sorted {
            (noteLookup[$0] ?? $0).localizedCaseInsensitiveCompare(noteLookup[$1] ?? $1) == .orderedAscending
        }
        let projectIds = Array(favorites.favoriteProjectIds).sorted {
            (projectLookup[$0] ?? $0).localizedCaseInsensitiveCompare(projectLookup[$1] ?? $1) == .orderedAscending
        }

        if !(noteIds.isEmpty && projectIds.isEmpty) {
            Section(isExpanded: $expanded) {
                ForEach(projectIds.prefix(Self.visibleCap), id: \.self) { id in
                    Label(projectLookup[id] ?? id, systemImage: "folder.fill")
                        .foregroundStyle(HygurColors.textPrimary)
                        .tag(SidebarItem.project(id))
                }
                ForEach(noteIds.prefix(Self.visibleCap), id: \.self) { id in
                    Label(noteLookup[id] ?? id, systemImage: "note.text")
                        .foregroundStyle(HygurColors.textPrimary)
                        .tag(SidebarItem.note(id))
                }

                let extra = max(0, noteIds.count + projectIds.count - Self.visibleCap)
                if extra > 0 {
                    Text("+\(extra) more")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textTertiary)
                        .listRowInsets(EdgeInsets(top: 2, leading: 12, bottom: 2, trailing: 8))
                }
            } header: {
                Button { expanded.toggle() } label: {
                    HStack(spacing: 4) {
                        Image(systemName: "star.fill")
                            .font(.caption2)
                            .foregroundStyle(HygurColors.brandGold)
                        Text("Favorites")
                            .font(.caption)
                            .fontWeight(.semibold)
                            .tracking(0.6)
                            .foregroundStyle(HygurColors.textTertiary)
                        Spacer()
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
            }
            .task(id: "\(noteIds.count)-\(projectIds.count)") {
                await refreshLookups()
            }
        }
    }

    private func refreshLookups() async {
        let sidecar = SidecarService.fromSettings()
        async let notesTask: [Note] = (try? await sidecar.listNotes()) ?? []
        async let projectsTask: [Project] = (try? await sidecar.listProjects()) ?? []
        let notes = await notesTask
        let projects = await projectsTask

        await MainActor.run {
            noteLookup = Dictionary(uniqueKeysWithValues: notes.map { ($0.id, $0.title) })
            projectLookup = Dictionary(uniqueKeysWithValues: projects.map { ($0.id, $0.name) })
        }
    }
}
