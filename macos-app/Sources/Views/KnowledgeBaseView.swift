import SwiftUI
import UniformTypeIdentifiers
import AppKit

struct KnowledgeBaseView: View {
    @State private var viewModel = KnowledgeBaseViewModel()
    @State private var showingImporter = false
    @State private var selectedItemId: String?
    @State private var searchText = ""
    @State private var quickLookItem: IdentifiableString?
    @State private var itemToDelete: KnowledgeItemResponse?
    @AppStorage("hygur.layout.knowledge") private var layoutModeRaw: String = ViewLayoutMode.list.rawValue
    @AppStorage("hygur.shortcut.quickLook") private var quickLookShortcutRaw: String = QuickLookShortcut.space.rawValue
    @Environment(InspectorSelection.self) private var inspector

    private var quickLookShortcut: QuickLookShortcut {
        QuickLookShortcut(rawValue: quickLookShortcutRaw) ?? .space
    }

    private var layoutMode: Binding<ViewLayoutMode> {
        Binding(
            get: { ViewLayoutMode(rawValue: layoutModeRaw) ?? .list },
            set: { layoutModeRaw = $0.rawValue }
        )
    }

    var body: some View {
        VStack(spacing: 0) {
            toolbar
            Divider()

            if viewModel.isSearchLoading {
                searchLoadingState
            } else if !searchText.isEmpty, let results = viewModel.searchResults {
                if results.isEmpty {
                    noSearchResultsState
                } else {
                    searchResultsList(results)
                }
            } else if viewModel.items.isEmpty && !viewModel.isLoading {
                emptyState
            } else {
                itemsList
            }
        }
        .toolbar {
            ToolbarItem(placement: .navigation) {
                ToolbarSearchField(text: $searchText, prompt: "Search documents")
            }
        }
        .fileImporter(
            isPresented: $showingImporter,
            allowedContentTypes: Self.supportedContentTypes,
            allowsMultipleSelection: true
        ) { result in
            handleFileImport(result)
        }
        .errorBannerOverlay($viewModel.error)
        .confirmationDialog(
            "Delete \"\(itemToDelete?.title ?? "item")\"?",
            isPresented: .constant(itemToDelete != nil),
            titleVisibility: .visible
        ) {
            Button("Delete", role: .destructive) {
                if let item = itemToDelete {
                    Task { await viewModel.deleteItem(item) }
                }
                itemToDelete = nil
            }
            Button("Cancel", role: .cancel) { itemToDelete = nil }
        }
        .sheet(item: $quickLookItem) { wrapper in
            DocumentQuickLookSheet(contentId: wrapper.value)
        }
        .task {
            await viewModel.loadItems()
        }
        .onChange(of: searchText) { _, newValue in
            viewModel.searchDebounced(query: newValue)
        }
        .onChange(of: selectedItemId) { _, newId in
            if let id = newId {
                inspector.current = .knowledgeItem(id)
            }
        }
    }

    // MARK: - Subviews

    private var toolbar: some View {
        FeatureHeader(title: "Knowledge Base", count: viewModel.totalCount) {
            if let progress = viewModel.importProgress {
                HStack(spacing: HygurSpacing.sm) {
                    LoadingIndicator(style: .small)
                    Text("Importing \(progress.current)/\(progress.total)")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                }
            } else if viewModel.isLoading {
                LoadingIndicator(style: .small)
            }

            ViewLayoutToggle(mode: layoutMode)

            IconButton(systemImage: "arrow.clockwise", label: "Refresh", action: {
                Task { await viewModel.loadItems() }
            }, isDisabled: viewModel.isLoading)

            Menu {
                Button {
                    showingImporter = true
                } label: {
                    Label("Import Files...", systemImage: "doc.badge.plus")
                }
                Button {
                    showFolderPicker()
                } label: {
                    Label("Import Folder...", systemImage: "folder.badge.plus")
                }
            } label: {
                Label("Import", systemImage: "plus")
            }
            .disabled(viewModel.isLoading)
        }
    }

    private func showFolderPicker() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        panel.message = "Select a folder to import"
        panel.prompt = "Import"

        if panel.runModal() == .OK, let url = panel.url {
            Task {
                await viewModel.importFolder(url)
            }
        }
    }

    private var emptyState: some View {
        EmptyStateView(
            icon: "doc.text.magnifyingglass",
            title: "No documents yet",
            subtitle: "Import documents to build your knowledge base",
            action: ("Import Files", { showingImporter = true })
        )
    }

    @ViewBuilder
    private var itemsList: some View {
        switch layoutMode.wrappedValue {
        case .list:
            listLayout
        case .grid:
            gridLayout
        }
    }

    private var listLayout: some View {
        List(selection: $selectedItemId) {
            ForEach(viewModel.items) { item in
                KnowledgeItemRow(item: item, projectName: viewModel.projectName(for: item.projectId))
                    .tag(item.id)
                    .contentShape(Rectangle())
                    .onTapGesture(count: 2) {
                        quickLookItem = IdentifiableString(item.id)
                    }
                    .contextMenu {
                        contextMenuContent(for: item)
                    }
            }

            if viewModel.hasMore {
                loadMoreRow
            }
        }
        .listStyle(.inset)
        // QuickLook shortcut is user-configurable in Settings → System.
        // Return is also accepted as the Finder-style "open" convention,
        // independent of the QuickLook shortcut setting.
        .onKeyPress(keys: Set([quickLookShortcut.keyEquivalent, .return])) { keyPress in
            guard let id = selectedItemId else { return .ignored }
            if keyPress.key == .return {
                quickLookItem = IdentifiableString(id)
                return .handled
            }
            if keyPress.key == quickLookShortcut.keyEquivalent {
                let hasShift = keyPress.modifiers.contains(.shift)
                if quickLookShortcut.requiresShift != hasShift { return .ignored }
                quickLookItem = IdentifiableString(id)
                return .handled
            }
            return .ignored
        }
        .onDrop(of: [.fileURL], isTargeted: nil) { providers in
            handleDrop(providers)
        }
        // Allow other views (AgendaSheet, search results, etc.) to open a
        // specific document by posting `.openDocument` with the content ID.
        .onReceive(NotificationCenter.default.publisher(for: .openDocument)) { notification in
            guard let id = notification.object as? String, !id.isEmpty else { return }
            selectedItemId = id
            quickLookItem = IdentifiableString(id)
        }
    }

    private var gridLayout: some View {
        ScrollView {
            LazyVGrid(
                columns: [GridItem(.adaptive(minimum: 240), spacing: HygurSpacing.sm)],
                spacing: HygurSpacing.sm
            ) {
                ForEach(viewModel.items) { item in
                    KnowledgeCard(
                        item: item,
                        projectName: viewModel.projectName(for: item.projectId),
                        fillContainer: true
                    )
                        .frame(maxWidth: .infinity, minHeight: 190, maxHeight: 190, alignment: .top)
                        .clipped()
                        .contentShape(Rectangle())
                        .onTapGesture(count: 2) {
                            quickLookItem = IdentifiableString(item.id)
                        }
                        .onTapGesture {
                            inspector.current = .knowledgeItem(item.id)
                        }
                        .contextMenu {
                            contextMenuContent(for: item)
                        }
                }

                if viewModel.hasMore {
                    loadMoreRow
                        .gridCellColumns(.max)
                        .onAppear { Task { await viewModel.loadNextPage() } }
                }
            }
            .padding(HygurSpacing.md)
        }
        .onDrop(of: [.fileURL], isTargeted: nil) { providers in
            handleDrop(providers)
        }
        .onReceive(NotificationCenter.default.publisher(for: .openDocument)) { notification in
            guard let id = notification.object as? String, !id.isEmpty else { return }
            quickLookItem = IdentifiableString(id)
        }
    }

    private var searchLoadingState: some View {
        VStack(spacing: HygurSpacing.md) {
            LoadingIndicator(style: .large)
            Text("Searching...")
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textSecondary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private var noSearchResultsState: some View {
        EmptyStateView(
            icon: "doc.text.magnifyingglass",
            title: "No results",
            subtitle: "No documents found for \"\(searchText)\""
        )
    }

    private func searchResultsList(_ results: [SearchResult]) -> some View {
        List(results) { result in
            SearchResultRow(result: result, query: searchText)
        }
        .listStyle(.inset)
    }

    private var loadMoreRow: some View {
        HStack {
            Spacer()
            if viewModel.isLoadingMore {
                ProgressView()
                    .controlSize(.small)
            } else {
                Button("Load More") {
                    Task { await viewModel.loadNextPage() }
                }
                .buttonStyle(.plain)
                .foregroundStyle(.secondary)
            }
            Text("\(viewModel.items.count) of \(viewModel.totalCount)")
                .font(.caption)
                .foregroundStyle(.tertiary)
            Spacer()
        }
        .padding(.vertical, 8)
        .onAppear {
            Task { await viewModel.loadNextPage() }
        }
    }

    @ViewBuilder
    private func contextMenuContent(for item: KnowledgeItemResponse) -> some View {
        Button {
            quickLookItem = IdentifiableString(item.id)
        } label: {
            Label("View / Edit", systemImage: "doc.text.magnifyingglass")
        }

        if let path = item.sourcePath {
            Button {
                NSWorkspace.shared.selectFile(path, inFileViewerRootedAtPath: "")
            } label: {
                Label("Show in Finder", systemImage: "folder")
            }

            Divider()
        }

        Button(role: .destructive) {
            itemToDelete = item
        } label: {
            Label("Delete", systemImage: "trash")
        }
    }

    // MARK: - File Import Handling

    private func handleFileImport(_ result: Result<[URL], Error>) {
        switch result {
        case .success(let urls):
            Task {
                await viewModel.importFiles(urls)
            }
        case .failure(let error):
            viewModel.error = error.localizedDescription
        }
    }

    // MARK: - Supported Content Types

    private static var supportedContentTypes: [UTType] {
        [
            .plainText,
            .pdf,
            UTType(filenameExtension: "md") ?? .plainText,
            UTType(filenameExtension: "markdown") ?? .plainText,
            UTType(filenameExtension: "docx") ?? .data,
            UTType(filenameExtension: "doc") ?? .data,
            .html,
            // Image types
            .png,
            .jpeg,
            UTType(filenameExtension: "heic") ?? .image,
            UTType(filenameExtension: "webp") ?? .image,
            // Audio types
            UTType(filenameExtension: "mp3") ?? .audio,
            UTType(filenameExtension: "m4a") ?? .audio,
            UTType(filenameExtension: "wav") ?? .audio,
            UTType(filenameExtension: "ogg") ?? .audio
        ]
    }

    // MARK: - Drag & Drop

    /// Extensions accepted via drag & drop into the knowledge base list.
    private static let dropExtensions: Set<String> = [
        "png", "jpg", "jpeg", "heic", "webp",
        "mp3", "m4a", "wav", "ogg"
    ]

    private func handleDrop(_ providers: [NSItemProvider]) -> Bool {
        var handled = false
        for provider in providers {
            if provider.hasItemConformingToTypeIdentifier(UTType.fileURL.identifier) {
                provider.loadItem(forTypeIdentifier: UTType.fileURL.identifier, options: nil) { item, _ in
                    guard
                        let data = item as? Data,
                        let url = URL(dataRepresentation: data, relativeTo: nil),
                        Self.dropExtensions.contains(url.pathExtension.lowercased())
                    else { return }

                    Task { @MainActor in
                        await viewModel.importFiles([url])
                    }
                }
                handled = true
            }
        }
        return handled
    }
}

// MARK: - Knowledge Item Row

struct KnowledgeItemRow: View {
    let item: KnowledgeItemResponse
    var projectName: String?

    var body: some View {
        KnowledgeCard(item: item, projectName: projectName)
            .padding(.vertical, HygurSpacing.xxs)
    }
}

// MARK: - Preview

#Preview {
    KnowledgeBaseView()
        .frame(width: 600, height: 400)
}
