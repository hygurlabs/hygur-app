import Foundation
import Observation

/// View model that polls `/insights/learning-progress` and exposes a
/// snapshot to the status bar gauge + the LearningInsightsView sheet.
///
/// Phase 1 of pair mode. Polling cadence is 60 s — the gauge changes only
/// when the user hits a milestone (a new memory type, a connector synced),
/// so faster polling adds noise without insight. We also refresh on
/// app-foreground via the `refresh()` entry point so the user sees an
/// up-to-date value when they look back at the window.
@MainActor
@Observable
final class LearningProgressViewModel {
    private(set) var progress: LearningProgressResponse?
    private(set) var lastFetchError: String?
    private(set) var isLoading: Bool = false

    private let service: () -> SidecarService
    private var pollingTask: Task<Void, Never>?

    init(service: @escaping () -> SidecarService = { SidecarService.fromSettings() }) {
        self.service = service
    }

    /// Convenience accessor — 0 when no snapshot has been fetched yet, so
    /// the bar renders empty rather than absent on first paint.
    var coverage: Double { progress?.coverage ?? 0 }

    /// Localised tooltip combining the percentage and the next-step hint.
    var tooltip: String {
        guard let p = progress else { return "Hygur is gathering signals…" }
        let percent = Int((p.coverage * 100).rounded())
        if p.nextStepHint.isEmpty { return "Hygur learning: \(percent)%" }
        return "Hygur learning: \(percent)% — next: \(p.nextStepHint)"
    }

    /// Start the 60 s background poll. Idempotent — calling twice does not
    /// create a second task. The first refresh runs immediately so the
    /// status bar paints a real value rather than 0.
    func startPolling() {
        guard pollingTask == nil else { return }
        pollingTask = Task { [weak self] in
            await self?.refresh()
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 60_000_000_000)
                if Task.isCancelled { break }
                await self?.refresh()
            }
        }
    }

    /// Stop the background poll. Called when the view tree tears down.
    func stopPolling() {
        pollingTask?.cancel()
        pollingTask = nil
    }

    /// Fetch the current snapshot. Public so callers (foreground hook,
    /// after-action refresh) can force an immediate update.
    func refresh() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let snapshot = try await service().learningProgress()
            self.progress = snapshot
            self.lastFetchError = nil
        } catch {
            // Don't blank the previous snapshot — a transient network blip
            // shouldn't make the gauge jump back to zero.
            self.lastFetchError = error.localizedDescription
        }
    }
}
