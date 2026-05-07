import Foundation
import Observation

/// `LMStudioStatus` mirrors the sidecar event payload values.
enum LMStudioStatus: String, Sendable {
    case unknown
    case up
    case down
}

/// `ActivityEvent` is a UI-friendly snapshot of a single sidecar event.
/// Includes the original `SidecarEvent` for typed-payload access plus a
/// stable identifier for `ForEach` rendering.
struct ActivityEvent: Identifiable, Sendable {
    let id: UUID
    let receivedAt: Date
    let raw: SidecarEvent

    init(raw: SidecarEvent) {
        self.id = UUID()
        self.receivedAt = Date()
        self.raw = raw
    }

    var type: String { raw.type }
    var source: String { raw.source }
    var status: String { raw.status }
    var message: String? { raw.message }
    var title: String {
        switch type {
        case "priority_mail":
            if let title = raw.string("title"), !title.isEmpty { return title }
            return "Priority email"
        case "mail_digest":
            let count = raw.int("count") ?? 0
            return count == 1 ? "1 nouvel email important" : "\(count) nouveaux emails importants"
        case "brief":
            if let date = raw.string("date") { return "Daily brief — \(date)" }
            return "Daily brief"
        case "lm_studio":
            return "LM Studio is \(raw.string("status") ?? status)"
        case "connectors":
            return "Connector \(source) — \(status)"
        case "ingest", "ingest_start", "ingest_progress", "ingest_complete":
            if let path = raw.string("path"), !path.isEmpty {
                let name = (path as NSString).lastPathComponent
                return "Ingest — \(name)"
            }
            return "Ingest — \(status)"
        case "sync":
            return "Sync — \(status)"
        case "sidecar_restart":
            return raw.message ?? "Sidecar restarted unexpectedly"
        case "chat_failed":
            return raw.message ?? "Chat reply failed"
        case "embedding_failed":
            return raw.message ?? "Embedding failed"
        default:
            return raw.message ?? "\(type) — \(status)"
        }
    }
}

/// `EventStreamService` is a long-lived consumer of `/events` SSE that fans
/// out typed updates to the rest of the app. Maintains a capped ring of
/// recent events for `ActivityView` and tracks LM Studio reachability for
/// the menubar status dot.
///
/// Reconnects with 1s/5s/30s backoff on disconnect.
@MainActor
@Observable
final class EventStreamService {
    /// Most recent first. Capped at `maxEvents`.
    private(set) var recentEvents: [ActivityEvent] = []
    /// Latest LM Studio status reported by the sidecar. Seeded from `/health`
    /// on every (re)connect and then kept in sync via `lm_studio` flip events.
    /// `.unknown` only if the initial `/health` probe fails and no flip has
    /// arrived yet.
    private(set) var lmStudioStatus: LMStudioStatus = .unknown
    /// True while the SSE connection is open; toggles the menubar dot when off.
    private(set) var sidecarConnected: Bool = false
    /// Wall-clock of the last app-side incident (sidecar restart, chat failure,
    /// embedding failure). The menu bar icon observes this to flash briefly so
    /// the user notices the failure even if they're not looking at the
    /// Activity panel. Reset to nil after the flash window expires.
    private(set) var lastIncidentAt: Date?

    /// External listeners (NotificationsService) subscribe via this closure
    /// hook — invoked once per received event on the main actor.
    var onEvent: ((ActivityEvent) -> Void)?

    private let maxEvents = 50
    private let backoffSchedule: [TimeInterval] = [1, 5, 30]
    private var task: Task<Void, Never>?
    private var sidecar: SidecarService?

    /// Starts the background consumer. Idempotent — calling twice is a no-op.
    func start(sidecar: SidecarService) {
        if task != nil { return }
        self.sidecar = sidecar
        task = Task { [weak self] in
            await self?.runLoop(sidecar: sidecar)
        }
    }

    /// Stops the consumer. The `recentEvents` snapshot is preserved.
    func stop() {
        task?.cancel()
        task = nil
        sidecarConnected = false
    }

    private func runLoop(sidecar: SidecarService) async {
        var attempt = 0
        while !Task.isCancelled {
            do {
                sidecarConnected = true
                attempt = 0
                // The sidecar's LMStudioWatcher only publishes events on
                // status *flips*. If the watcher already transitioned to
                // up/down before we connected (the common case at app
                // launch), no event will be replayed — so we'd be stuck on
                // .unknown until the next flip. Seed from /health to surface
                // the current state immediately.
                await seedLMStudioStatus(sidecar: sidecar)
                for try await event in await sidecar.streamEvents() {
                    if Task.isCancelled { break }
                    handle(event)
                }
                // Stream ended cleanly — wait briefly and retry.
            } catch {
                // ignore the error itself; it's typically a network drop
            }
            sidecarConnected = false
            if Task.isCancelled { break }
            let wait = backoffSchedule[min(attempt, backoffSchedule.count - 1)]
            attempt += 1
            try? await Task.sleep(nanoseconds: UInt64(wait * 1_000_000_000))
        }
    }

    /// Probes `/health` once and maps the `lm_studio` field onto
    /// `lmStudioStatus`. Silent on failure — SSE flip events still apply.
    private func seedLMStudioStatus(sidecar: SidecarService) async {
        do {
            let resp = try await sidecar.health()
            switch resp.lmStudio {
            case "connected":
                lmStudioStatus = .up
            case "disconnected":
                lmStudioStatus = .down
            default:
                break
            }
        } catch {
            // /health unreachable — leave status as-is and let the next
            // flip event (or reconnect) fix it.
        }
    }

    private func handle(_ raw: SidecarEvent) {
        // The sidecar emits {"type":"connection","message":"connected"} as
        // an SSE hello on every client connect. With the reconnect-with-backoff
        // loop this would pile up "connected" rows in the activity log every
        // few seconds — pure transport noise, not user-relevant activity.
        // The `sidecarConnected` flag already tracks the same state for the
        // menubar, so we just drop these.
        if raw.type == "connection" {
            return
        }

        // Update the LM Studio status field eagerly so the UI reflects flips.
        if raw.type == "lm_studio", let s = raw.string("status") {
            lmStudioStatus = LMStudioStatus(rawValue: s) ?? .unknown
        }

        let activityEvent = ActivityEvent(raw: raw)
        recentEvents.insert(activityEvent, at: 0)
        if recentEvents.count > maxEvents {
            recentEvents.removeLast(recentEvents.count - maxEvents)
        }
        onEvent?(activityEvent)
    }

    /// Record an app-side incident (sidecar restart, chat failure, embedding
    /// failure) into the Activity feed. Use this from places that catch a
    /// failure path the sidecar doesn't itself report — the user shouldn't
    /// have to dig into Console.app to know that something broke.
    ///
    /// Also bumps `lastIncidentAt` so the menu bar icon can flash briefly.
    /// The flash window is short (3 s) on purpose: incidents that persist
    /// already get a sticky banner (`RuntimeUnreachableBanner`) — this is
    /// just for the transient ones the user might otherwise miss.
    func recordLocalIncident(type: String, message: String, source: String = "app") {
        let raw = SidecarEvent(
            type: type,
            source: source,
            status: "failed",
            message: message
        )
        handle(raw)
        lastIncidentAt = Date()
        Task { [weak self] in
            try? await Task.sleep(nanoseconds: 3_000_000_000)
            await self?.clearIncidentFlashIfStale()
        }
    }

    private func clearIncidentFlashIfStale() {
        guard let at = lastIncidentAt,
              Date().timeIntervalSince(at) >= 3 else { return }
        lastIncidentAt = nil
    }
}
