import Foundation
import SwiftUI

/// Drives `MemoryTimelineView`. Holds the current query and the chaptered
/// response, debounces user input so we don't fire a request on every
/// keystroke, and tracks which chapter the user has expanded.
@MainActor
@Observable
final class TimelineViewModel {
    /// Free-form topic the user is exploring (e.g. "TVA Q1", "Jean").
    var query: String = ""
    /// Latest sidecar response — never nil after a successful call, but
    /// `chapters` may be empty when the topic doesn't match anything.
    var chapters: [TimelineChapter] = []
    var isLoading = false
    var error: String?
    /// Chapter currently dropdown-expanded in the UI. nil = all collapsed.
    var expandedChapterID: String?
    /// Total events across the response — handy for "showing X events" labels.
    var totalEvents: Int = 0
    /// Default: 365 days. Lower numbers reduce noise on long-lived topics.
    var rangeDays: Int = 365

    private let service: SidecarService
    private var pendingTask: Task<Void, Never>?
    /// Roughly matches typing speed — long enough to skip mid-word reflows,
    /// short enough that a paste lands a request quickly.
    private let debounceNanos: UInt64 = 300_000_000

    init(service: SidecarService = .fromSettings()) {
        self.service = service
    }

    /// Schedules a debounced search. Each call cancels the previous pending
    /// task so only the last query in a 300 ms window actually runs.
    func searchDebounced() {
        pendingTask?.cancel()
        let q = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !q.isEmpty else {
            chapters = []
            totalEvents = 0
            error = nil
            isLoading = false
            return
        }
        pendingTask = Task { [weak self] in
            guard let self else { return }
            try? await Task.sleep(nanoseconds: self.debounceNanos)
            if Task.isCancelled { return }
            await self.runSearch(q)
        }
    }

    /// Fires a search immediately, bypassing the debounce. Used by the
    /// "tap entity to refine" flow where the user expects an instant reload.
    func searchNow() {
        pendingTask?.cancel()
        let q = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !q.isEmpty else {
            chapters = []
            totalEvents = 0
            return
        }
        Task { await runSearch(q) }
    }

    /// Toggles which chapter is expanded. Tapping the active one collapses it.
    func toggle(_ chapter: TimelineChapter) {
        if expandedChapterID == chapter.id {
            expandedChapterID = nil
        } else {
            expandedChapterID = chapter.id
        }
    }

    private func runSearch(_ q: String) async {
        isLoading = true
        error = nil
        defer { isLoading = false }
        do {
            let resp = try await service.timelineQuery(q, rangeDays: rangeDays)
            if Task.isCancelled { return }
            chapters = resp.chapters
            totalEvents = resp.totalEvents
            // Auto-expand the most recent chapter so the user sees content
            // without a second click on small responses.
            if chapters.count == 1 {
                expandedChapterID = chapters.first?.id
            } else {
                expandedChapterID = nil
            }
        } catch {
            self.error = error.localizedDescription
            self.chapters = []
            self.totalEvents = 0
        }
    }
}
