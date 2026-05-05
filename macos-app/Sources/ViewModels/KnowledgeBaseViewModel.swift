import Foundation
import SwiftUI

@MainActor
@Observable
final class KnowledgeBaseViewModel {
    var items: [KnowledgeItemResponse] = []
    var projects: [Project] = []
    var isLoading = false
    var isLoadingMore = false
    var error: String?
    var importProgress: ImportProgress?
    var searchResults: [SearchResult]? = nil
    var isSearchLoading = false
    private var searchDebounceTask: Task<Void, Never>?

    private(set) var totalCount: Int = 0
    private(set) var pageSize: Int = 50
    private(set) var loadedOffset: Int = 0

    var hasMore: Bool { items.count < totalCount }

    private let sidecarService: SidecarService

    struct ImportProgress: Equatable {
        let current: Int
        let total: Int
        let currentFileName: String
    }

    init(sidecarService: SidecarService = .fromSettings()) {
        self.sidecarService = sidecarService
    }

    func loadItems() async {
        isLoading = true
        error = nil
        defer { isLoading = false }

        do {
            async let listTask = sidecarService.listKnowledgeItems(limit: pageSize, offset: 0)
            async let projectsTask = sidecarService.listProjects()

            let (listResponse, loadedProjects) = try await (listTask, projectsTask)

            items = listResponse.items
            totalCount = listResponse.total
            loadedOffset = listResponse.items.count
            projects = loadedProjects
        } catch {
            self.error = error.localizedDescription
        }
    }

    func loadNextPage() async {
        guard hasMore, !isLoadingMore, !isLoading else { return }

        isLoadingMore = true
        defer { isLoadingMore = false }

        do {
            let listResponse = try await sidecarService.listKnowledgeItems(limit: pageSize, offset: loadedOffset)
            let existingIDs = Set(items.map { $0.id })
            let newItems = listResponse.items.filter { !existingIDs.contains($0.id) }
            items.append(contentsOf: newItems)
            totalCount = listResponse.total
            loadedOffset += newItems.count
        } catch {
            self.error = error.localizedDescription
        }
    }

    func projectName(for projectId: String?) -> String? {
        guard let projectId = projectId else { return nil }
        return projects.first { $0.id == projectId }?.name
    }

    func updateItem(_ updatedItem: KnowledgeItemResponse) {
        if let index = items.firstIndex(where: { $0.id == updatedItem.id }) {
            items[index] = updatedItem
        }
    }

    func importFiles(_ urls: [URL]) async {
        guard !urls.isEmpty else { return }

        isLoading = true
        error = nil
        defer {
            isLoading = false
            importProgress = nil
        }

        for (index, url) in urls.enumerated() {
            importProgress = ImportProgress(
                current: index + 1,
                total: urls.count,
                currentFileName: url.lastPathComponent
            )

            do {
                let didStartAccessing = url.startAccessingSecurityScopedResource()
                defer {
                    if didStartAccessing {
                        url.stopAccessingSecurityScopedResource()
                    }
                }

                let response = try await sidecarService.ingestFile(path: url.path)

                let formatter = ISO8601DateFormatter()
                    formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
                    let now = formatter.string(from: Date())

                    let newItem = KnowledgeItemResponse(
                        contentId: response.contentId,
                        sourceType: sourceTypeFromPath(url.path),
                        sourcePath: url.path,
                        title: response.title,
                        normalizedText: nil,
                        chunkCount: response.chunkCount,
                        tags: [],
                        projectId: nil,
                        createdAt: now,
                        updatedAt: now,
                        date: nil
                    )

                if response.status == .indexed && !items.contains(where: { $0.id == newItem.id }) {
                    items.insert(newItem, at: 0)
                    totalCount += 1
                    loadedOffset += 1
                }
            } catch {
                self.error = "Failed to import \(url.lastPathComponent): \(error.localizedDescription)"
            }
        }
    }

    func importFolder(_ url: URL) async {
        isLoading = true
        error = nil
        defer {
            isLoading = false
            importProgress = nil
        }

        do {
            let didStartAccessing = url.startAccessingSecurityScopedResource()
            defer {
                if didStartAccessing {
                    url.stopAccessingSecurityScopedResource()
                }
            }

            importProgress = ImportProgress(
                current: 0,
                total: 1,
                currentFileName: "Scanning \(url.lastPathComponent)..."
            )

            let response = try await sidecarService.ingestFolder(path: url.path)

            // Reload from scratch to get fresh page 1
            let listResponse = try await sidecarService.listKnowledgeItems(limit: pageSize, offset: 0)
            items = listResponse.items
            totalCount = listResponse.total
            loadedOffset = listResponse.items.count

            if response.failed > 0 {
                self.error = "Imported \(response.processed) files, \(response.failed) failed"
            }
        } catch {
            self.error = "Failed to import folder: \(error.localizedDescription)"
        }
    }

    func deleteItem(_ item: KnowledgeItemResponse) async {
        do {
            try await sidecarService.deleteKnowledgeItem(contentId: item.contentId)
            items.removeAll { $0.id == item.id }
            totalCount = max(0, totalCount - 1)
            loadedOffset = max(0, loadedOffset - 1)
        } catch {
            self.error = "Failed to delete \(item.title): \(error.localizedDescription)"
        }
    }

    func searchDebounced(query: String) {
        searchDebounceTask?.cancel()
        if query.isEmpty {
            searchResults = nil
            return
        }
        searchDebounceTask = Task {
            try? await Task.sleep(nanoseconds: 300_000_000)
            guard !Task.isCancelled else { return }
            await search(query: query)
        }
    }

    private func search(query: String) async {
        isSearchLoading = true
        error = nil
        defer { isSearchLoading = false }
        do {
            let response = try await sidecarService.searchKnowledge(query: query, topK: 20)
            searchResults = response.results
        } catch {
            self.error = error.localizedDescription
            searchResults = []
        }
    }

    func clearError() {
        error = nil
    }

    // MARK: - Helpers

    private func sourceTypeFromPath(_ path: String) -> String {
        let ext = (path as NSString).pathExtension.lowercased()
        switch ext {
        case "md", "markdown": return "markdown"
        case "pdf": return "pdf"
        case "docx", "doc": return "docx"
        case "txt": return "txt"
        case "html", "htm": return "html"
        default: return "unknown"
        }
    }
}
