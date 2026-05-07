import SwiftUI

/// Maps a `SidebarItem` selection onto the detail column of the
/// `NavigationSplitView`. Centralising the switch here keeps `ContentView`
/// lean and makes it easy to plug in deep-linked detail surfaces (notes,
/// projects) without touching the split-view scaffolding.
@MainActor
enum ColumnRouter {

    @ViewBuilder
    static func main(
        for selection: SidebarItem?,
        chatViewModel: ChatViewModel,
        showingNewNote: Binding<Bool>
    ) -> some View {
        switch selection {
        case .newChat, .none:
            ChatView(viewModel: chatViewModel)
        case .chatSession(let sessionId):
            // `.id(sessionId)` forces SwiftUI to recreate ChatView so each
            // session keeps its own scroll, composer state, and stream lifecycle.
            ChatView(viewModel: chatViewModel)
                .id(sessionId)
        case .note(let id):
            NoteDetailLoader(noteId: id)
        case .project(let id):
            ProjectDetailLoader(projectId: id)
        case .notes:
            NotesView(showingNewNote: showingNewNote)
        case .projects:
            ProjectListView()
        case .knowledgeBase:
            KnowledgeBaseView()
        case .search:
            SearchView()
        case .tags:
            TagsView()
        case .email:
            EmailThreadsView()
        case .graph:
            MemoryTimelineView()
        case .connectors:
            ConnectorsView()
        case .marketplace:
            ConnectorMarketplaceView()
        case .activity:
            ActivityView()
        case .memories:
            MemoriesView()
        }
    }
}
