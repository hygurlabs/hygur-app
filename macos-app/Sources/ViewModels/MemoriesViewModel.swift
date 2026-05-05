import Foundation
import SwiftUI

/// Drives `MemoriesView`: loads the full memory list from the sidecar and
/// supports per-row deletion. Group counts are derived live from `memories`
/// so the UI always reflects the truth.
@MainActor
@Observable
final class MemoriesViewModel {
    var memories: [MemoryItem] = []
    var isLoading = false
    var error: String?

    private let service: SidecarService

    init(service: SidecarService = .fromSettings()) {
        self.service = service
    }

    func load() async {
        isLoading = true
        error = nil
        defer { isLoading = false }
        do {
            let raw = try await service.listMemories()
            // Sidecar returns oldest-first; UX expects newest-first.
            memories = raw.sorted { $0.createdAt > $1.createdAt }
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// Optimistically removes the row, then asks the sidecar to delete.
    /// Reloads on failure so the UI stays consistent.
    func delete(_ memory: MemoryItem) async {
        let snapshot = memories
        memories.removeAll { $0.memoryId == memory.memoryId }
        do {
            try await service.deleteMemory(id: memory.memoryId)
        } catch {
            self.error = error.localizedDescription
            memories = snapshot
        }
    }

    var groupedByKind: [(MemoryKind, [MemoryItem])] {
        let order: [MemoryKind] = [.fact, .preference, .action]
        return order.compactMap { kind in
            let items = memories.filter { MemoryKind(raw: $0.type) == kind }
            return items.isEmpty ? nil : (kind, items)
        }
    }
}
