import Foundation
import Combine

@MainActor
class SearchViewModel: ObservableObject {
    @Published var query: String = ""
    @Published var results: [SearchResult] = []
    @Published var isSearching = false
    @Published var error: String?
    @Published var dateFrom: Date?
    @Published var dateTo: Date?

    /// Available projects for the focus filter (loaded lazily once).
    @Published var availableProjects: [Project] = []
    /// When set, search is restricted to documents linked to this project.
    /// Nil means unscoped search across the full corpus.
    @Published var projectFilterId: String?

    private var searchTask: Task<Void, Never>?
    private var debounceTask: Task<Void, Never>?
    private var projectsLoaded = false

    func searchDebounced() {
        debounceTask?.cancel()
        debounceTask = Task {
            try? await Task.sleep(nanoseconds: 300_000_000)  // 300ms
            guard !Task.isCancelled else { return }
            await search()
        }
    }

    func search() async {
        let trimmedQuery = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedQuery.isEmpty else {
            results = []
            return
        }

        searchTask?.cancel()
        isSearching = true
        error = nil

        do {
            let sidecar = SidecarService.fromSettings()
            let response = try await sidecar.searchKnowledge(
                query: trimmedQuery,
                projectId: projectFilterId,
                dateFrom: dateFrom,
                dateTo: dateTo,
                topK: 20
            )
            if !Task.isCancelled {
                results = response.results
            }
        } catch {
            if !Task.isCancelled {
                self.error = error.localizedDescription
            }
        }

        isSearching = false
    }

    func clearSearch() {
        query = ""
        results = []
        error = nil
        searchTask?.cancel()
        debounceTask?.cancel()
    }

    /// Loads the list of projects once on demand, so the focus picker has
    /// data to display without doing it eagerly at app launch.
    func loadProjectsIfNeeded() async {
        guard !projectsLoaded else { return }
        projectsLoaded = true
        do {
            let sidecar = SidecarService.fromSettings()
            availableProjects = try await sidecar.listProjects()
        } catch {
            // Non-fatal: leaves the picker showing only "Tous" so the user
            // can still search unscoped.
            projectsLoaded = false
        }
    }
}
