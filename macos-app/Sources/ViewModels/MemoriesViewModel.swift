import Foundation
import SwiftUI

/// Drives `MemoriesView`: loads accepted memories AND the pending-review
/// queue from the sidecar in parallel, then exposes accept/discard/delete
/// actions. Group counts are derived live from `acceptedMemories` so the UI
/// always reflects the truth.
///
/// Phase 3.3 split: extracted memories with `acceptedAt == nil` live in
/// `pendingMemories` and surface in the "Pending review" section. Until the
/// user accepts them, the chat handler's `SearchAccepted` filters them out
/// so the LLM never sees an unreviewed candidate.
@MainActor
@Observable
final class MemoriesViewModel {
    /// Accepted memories (manual + accepted extractions). Drives the main
    /// grouped list. Newest-first.
    var acceptedMemories: [MemoryItem] = []
    /// Candidate memories awaiting review. Shown in the dedicated section
    /// at the top of `MemoriesView`. Newest-first.
    var pendingMemories: [MemoryItem] = []
    var isLoading = false
    var error: String?

    private let service: SidecarService

    init(service: SidecarService = .fromSettings()) {
        self.service = service
    }

    /// Backwards-compatible accessor — some call sites (Spotlight indexer
    /// preview, count badges) just want "everything". Pending first so a
    /// total count reflects "things the user can act on".
    var memories: [MemoryItem] {
        pendingMemories + acceptedMemories
    }

    func load() async {
        isLoading = true
        error = nil
        defer { isLoading = false }

        // Parallel fetch — the two endpoints are independent and the pending
        // list is usually short. Saves one round-trip on the spinner path.
        async let allTask = service.listMemories()
        async let pendingTask = service.listPendingMemories()
        do {
            let (raw, pending) = try await (allTask, pendingTask)
            // /memory/list returns BOTH accepted and pending rows. Filter the
            // pending out client-side so they only appear once (in the
            // dedicated section). Sidecar returns oldest-first; UX expects
            // newest-first.
            let pendingIDs = Set(pending.map { $0.memoryId })
            acceptedMemories = raw
                .filter { !pendingIDs.contains($0.memoryId) }
                .sorted { $0.createdAt > $1.createdAt }
            pendingMemories = pending.sorted { $0.createdAt > $1.createdAt }
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// Optimistically removes the row, then asks the sidecar to delete.
    /// Reloads on failure so the UI stays consistent.
    func delete(_ memory: MemoryItem) async {
        let acceptedSnapshot = acceptedMemories
        let pendingSnapshot = pendingMemories
        acceptedMemories.removeAll { $0.memoryId == memory.memoryId }
        pendingMemories.removeAll { $0.memoryId == memory.memoryId }
        do {
            try await service.deleteMemory(id: memory.memoryId)
        } catch {
            self.error = error.localizedDescription
            acceptedMemories = acceptedSnapshot
            pendingMemories = pendingSnapshot
        }
    }

    /// Promote a pending candidate to accepted. The sidecar stamps
    /// `accepted_at` and the row becomes eligible for chat injection.
    func accept(_ memory: MemoryItem) async {
        let pendingSnapshot = pendingMemories
        let acceptedSnapshot = acceptedMemories
        pendingMemories.removeAll { $0.memoryId == memory.memoryId }
        // Optimistically promote so the row shows up in its kind section.
        let now = ISO8601DateFormatter().string(from: Date())
        let promoted = MemoryItem(
            memoryId: memory.memoryId,
            type: memory.type,
            content: memory.content,
            createdAt: memory.createdAt,
            source: memory.source,
            acceptedAt: now,
            sessionId: memory.sessionId
        )
        acceptedMemories.insert(promoted, at: 0)
        do {
            try await service.acceptMemory(id: memory.memoryId)
        } catch {
            self.error = error.localizedDescription
            pendingMemories = pendingSnapshot
            acceptedMemories = acceptedSnapshot
        }
    }

    /// Reject a pending candidate. The sidecar deletes the row outright.
    func discard(_ memory: MemoryItem) async {
        let pendingSnapshot = pendingMemories
        pendingMemories.removeAll { $0.memoryId == memory.memoryId }
        do {
            try await service.discardMemory(id: memory.memoryId)
        } catch {
            self.error = error.localizedDescription
            pendingMemories = pendingSnapshot
        }
    }

    var groupedByKind: [(MemoryKind, [MemoryItem])] {
        let order: [MemoryKind] = [.fact, .preference, .action]
        return order.compactMap { kind in
            let items = acceptedMemories.filter { MemoryKind(raw: $0.type) == kind }
            return items.isEmpty ? nil : (kind, items)
        }
    }
}
