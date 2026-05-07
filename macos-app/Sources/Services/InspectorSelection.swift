import Foundation
import Observation

/// Cross-view "what's currently selected in the active list view".
/// `PropertiesPanel` reads from this so single-clicking a row in
/// `NotesView` / `KnowledgeBaseView` / `EmailThreadsView` populates the
/// right-hand inspector — the sidebar selection alone isn't enough since
/// those views own their own list state.
@MainActor
@Observable
final class InspectorSelection {
    enum Entity: Equatable {
        case note(String)
        case project(String)
        case knowledgeItem(String)
        /// Carries the full thread because the sidecar doesn't expose a
        /// "fetch single thread by id" endpoint — threads come bundled
        /// with the account-scoped list call.
        case mailThread(EmailThread)
    }

    var current: Entity?

    /// Clears the inspector when the user navigates away from a view that
    /// owned the selection (e.g. switching tabs).
    func clear() { current = nil }
}
