import Foundation
import Observation

/// Local store for favorited notes and projects. Persisted to `UserDefaults`
/// because the sidecar API does not yet have an `is_favorite` column on
/// either entity. The store is exposed via `@Environment(FavoritesStore.self)`
/// from `HygurApp`, mirroring how `EventStreamService` and `SidecarSupervisor`
/// are injected. The day the sidecar starts persisting favorites, swap the
/// implementation here without touching call sites.
@Observable
final class FavoritesStore {
    private(set) var favoriteNoteIds: Set<String>
    private(set) var favoriteProjectIds: Set<String>

    private let notesKey = "hygur.favorites.notes"
    private let projectsKey = "hygur.favorites.projects"

    init() {
        self.favoriteNoteIds = Self.loadSet(forKey: notesKey)
        self.favoriteProjectIds = Self.loadSet(forKey: projectsKey)
    }

    // MARK: - Notes

    func isFavorite(noteId: String) -> Bool {
        favoriteNoteIds.contains(noteId)
    }

    func toggleNote(_ id: String) {
        if favoriteNoteIds.contains(id) {
            favoriteNoteIds.remove(id)
        } else {
            favoriteNoteIds.insert(id)
        }
        Self.persist(favoriteNoteIds, forKey: notesKey)
    }

    // MARK: - Projects

    func isFavorite(projectId: String) -> Bool {
        favoriteProjectIds.contains(projectId)
    }

    func toggleProject(_ id: String) {
        if favoriteProjectIds.contains(id) {
            favoriteProjectIds.remove(id)
        } else {
            favoriteProjectIds.insert(id)
        }
        Self.persist(favoriteProjectIds, forKey: projectsKey)
    }

    // MARK: - Persistence

    private static func loadSet(forKey key: String) -> Set<String> {
        guard let data = UserDefaults.standard.data(forKey: key),
              let array = try? JSONDecoder().decode([String].self, from: data) else {
            return []
        }
        return Set(array)
    }

    private static func persist(_ set: Set<String>, forKey key: String) {
        guard let data = try? JSONEncoder().encode(Array(set)) else { return }
        UserDefaults.standard.set(data, forKey: key)
    }
}
