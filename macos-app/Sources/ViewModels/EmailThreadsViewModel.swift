import Foundation
import SwiftUI

@MainActor
@Observable
final class EmailThreadsViewModel {
    // Multi-account state.
    var accounts: [MailAccount] = []
    var selectedAccountId: String? = nil

    var threads: [EmailThread] = []
    var labels: [MailLabel] = []
    var selectedLabelIDs: Set<String> = []
    var isLoading = false
    var isSyncing = false
    var error: String?

    let sidecarService: SidecarService

    /// Source for the currently selected account, used by per-source endpoints
    /// (index/summarize/labels) that still take a provider name. Resolves to
    /// the provider of the selected account, or nil if no account is selected.
    var selectedSourceLegacy: String? {
        guard let id = selectedAccountId else { return nil }
        return accounts.first(where: { $0.accountId == id })?.provider
    }

    /// Synced thread count for the currently selected account.
    var selectedAccountThreadCount: Int {
        guard let id = selectedAccountId else { return 0 }
        return accounts.first(where: { $0.accountId == id })?.threadCount ?? 0
    }

    init(sidecarService: SidecarService = .fromSettings()) {
        self.sidecarService = sidecarService
    }

    // MARK: - Load Accounts

    /// Loads the configured mail accounts and auto-selects the first
    /// connected one when nothing is currently selected.
    func loadAccounts() async {
        isLoading = true
        error = nil
        defer { isLoading = false }

        do {
            accounts = try await sidecarService.listMailAccounts()
            if selectedAccountId == nil {
                selectedAccountId = accounts.first(where: { $0.isConnected })?.accountId
                    ?? accounts.first?.accountId
            }
        } catch {
            self.error = error.localizedDescription
        }
    }

    // MARK: - Load Threads

    func loadThreads() async {
        guard let accountId = selectedAccountId else {
            threads = []
            return
        }

        isLoading = true
        error = nil
        defer { isLoading = false }

        do {
            threads = try await sidecarService.listMailThreads(
                accountId: accountId,
                labelIDs: Array(selectedLabelIDs),
                limit: 50,
                offset: 0
            )
        } catch {
            self.error = error.localizedDescription
        }
    }

    // MARK: - Trigger Full Sync

    /// Kicks off a full sync (folders + labels + emails) on the selected
    /// account in the background. The UI subscribes to the events stream to
    /// learn when the sync completes — at which point loadAccounts() and
    /// loadThreads() are re-issued so the count badge and the list reflect
    /// the new state.
    func triggerFullSync() async {
        guard let accountId = selectedAccountId else { return }
        isSyncing = true
        error = nil

        do {
            _ = try await sidecarService.triggerMailSync(accountId: accountId, full: true, async: true)
            // The completion is observed via the event broker; meanwhile we
            // poll the account list once after a short delay so the count
            // badge updates promptly even if the SSE stream is unavailable.
            await waitForSyncCompletion(accountId: accountId)
        } catch {
            self.error = "Sync failed: \(error.localizedDescription)"
        }
        isSyncing = false
    }

    /// Polls the account stats until the count stabilises or a timeout
    /// elapses. This is a stop-gap until SSE event subscription is wired
    /// into the macOS client; once SSE is in place this can become a
    /// cancellable observer of `connector_id=mail` completion events.
    ///
    /// Timeout is 10 minutes — large Gmail syncs with embedding can take
    /// several minutes when the LM Studio instance is remote.
    private func waitForSyncCompletion(accountId: String, timeout: TimeInterval = 600) async {
        let deadline = Date().addingTimeInterval(timeout)
        var lastCount = selectedAccountThreadCount
        var stableTicks = 0
        while Date() < deadline {
            try? await Task.sleep(nanoseconds: 5_000_000_000) // poll every 5s
            await loadAccounts()
            let current = accounts.first(where: { $0.accountId == accountId })?.threadCount ?? lastCount
            if current == lastCount {
                stableTicks += 1
                if stableTicks >= 3 { break } // 3 stable polls (~15s) = sync settled
            } else {
                stableTicks = 0
                lastCount = current
            }
        }
        await loadThreads()
    }

    // MARK: - Index Thread

    func indexThread(_ thread: EmailThread) async -> Bool {
        guard let source = selectedSourceLegacy else { return false }

        do {
            try await sidecarService.indexMailThread(source: source, threadId: thread.id)
            return true
        } catch {
            self.error = "Failed to index thread: \(error.localizedDescription)"
            return false
        }
    }

    // MARK: - Summarize Thread

    func summarizeThread(_ thread: EmailThread, model: String) async -> EmailSummary? {
        guard let source = selectedSourceLegacy else { return nil }

        do {
            return try await sidecarService.summarizeMailThread(
                source: source,
                threadId: thread.id,
                model: model
            )
        } catch {
            self.error = "Failed to summarize thread: \(error.localizedDescription)"
            return nil
        }
    }

    // MARK: - Load Labels

    func loadLabels() async {
        guard let accountId = selectedAccountId else {
            labels = []
            return
        }

        do {
            labels = try await sidecarService.listMailLabels(accountId: accountId)
        } catch {
            labels = []
        }
    }

    // MARK: - Helpers

    func clearError() {
        error = nil
    }

    /// Updates the selected account and reloads dependent data.
    func selectAccount(_ accountId: String?) async {
        selectedAccountId = accountId
        selectedLabelIDs = []
        if accountId != nil {
            await loadThreads()
            await loadLabels()
        } else {
            threads = []
            labels = []
        }
    }
}
