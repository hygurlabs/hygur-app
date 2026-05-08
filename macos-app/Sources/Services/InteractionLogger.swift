import Foundation

/// Phase 1 (pair mode) — typed front-end for the sidecar's append-only
/// interaction_log. Every method is fire-and-forget by design: a logging
/// failure must never block, slow down, or leak into the user-facing flow.
/// Errors are swallowed silently after a debug print — the source of truth
/// for delivery is the sidecar's HTTP log.
///
/// Keep the kind strings here in sync with `interactions.Kind` in
/// `sidecar/internal/interactions/log.go`. Adding a kind here without
/// declaring it server-side will trip the 400 "unknown interaction kind"
/// validator and the event will be dropped.
@MainActor
final class InteractionLogger {
    static let shared = InteractionLogger()

    /// Resolved lazily so the logger can be constructed during app startup
    /// before the sidecar URL or auth token are known.
    private var serviceProvider: () -> SidecarService? = { nil }

    private init() {}

    /// Wire the SidecarService source. Called once from `HygurApp` after the
    /// service is constructed so every subsequent call routes through the
    /// canonical instance.
    func configure(_ provider: @escaping () -> SidecarService?) {
        self.serviceProvider = provider
    }

    // MARK: - Typed entry points

    func chatMessageSent(sessionId: String? = nil) {
        send(kind: "chat_message_sent", sessionId: sessionId)
    }

    func chatMessageReceived(sessionId: String? = nil) {
        send(kind: "chat_message_received", sessionId: sessionId)
    }

    func briefOpened(briefId: String) {
        send(kind: "brief_opened", refKind: "brief", refId: briefId)
    }

    func briefDismissed(briefId: String) {
        send(kind: "brief_dismissed", refKind: "brief", refId: briefId)
    }

    func memoryAccepted(memoryId: String, type: String? = nil) {
        var payload: [String: String] = [:]
        if let type, !type.isEmpty { payload["type"] = type }
        send(kind: "memory_accepted", refKind: "memory", refId: memoryId, payload: payload.isEmpty ? nil : payload)
    }

    func memoryDiscarded(memoryId: String) {
        send(kind: "memory_discarded", refKind: "memory", refId: memoryId)
    }

    func memorySuperseded(memoryId: String, supersededBy: String) {
        send(
            kind: "memory_superseded",
            refKind: "memory",
            refId: memoryId,
            payload: ["superseded_by": supersededBy]
        )
    }

    func documentOpened(contentId: String) {
        send(kind: "document_opened", refKind: "knowledge_item", refId: contentId)
    }

    func agendaActionCompleted(sourceId: String) {
        send(kind: "agenda_action_completed", refKind: "knowledge_item", refId: sourceId)
    }

    /// Log a successful connector sync. `connectorId` should be a stable id
    /// (not the user-visible label) so distinct-count queries are accurate.
    func connectorSynced(connectorId: String) {
        send(kind: "connector_synced", refKind: "connector", refId: connectorId)
    }

    func appLaunched() {
        send(kind: "app_launched")
    }

    // MARK: - Internal

    private func send(
        kind: String,
        refKind: String? = nil,
        refId: String? = nil,
        payload: [String: String]? = nil,
        sessionId: String? = nil
    ) {
        guard let service = serviceProvider() else { return }
        Task.detached(priority: .background) {
            do {
                try await service.logInteraction(
                    kind: kind,
                    refKind: refKind,
                    refId: refId,
                    payload: payload,
                    sessionId: sessionId
                )
            } catch {
                #if DEBUG
                print("InteractionLogger: \(kind) drop — \(error)")
                #endif
            }
        }
    }
}
