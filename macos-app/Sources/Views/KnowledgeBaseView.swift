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
        .searchable(text: $searchText, prompt: "Search documents")
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

    private var itemsList: some View {
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
        // Space bar opens QuickLook for the selected item.
        .onKeyPress(.space) {
            guard let id = selectedItemId else { return .ignored }
            quickLookItem = IdentifiableString(id)
            return .handled
        }
        .onDrop(of: [.fileURL], isTargeted: nil) { providers in
            handleDrop(providers)
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
            Label("Voir / Éditer", systemImage: "doc.text.magnifyingglass")
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

    /// Maximum number of tags to display before showing "+N"
    private let maxVisibleTags = 3

    var body: some View {
        HStack {
            sourceTypeLeadingView
                .frame(width: 40, height: 40)

            VStack(alignment: .leading, spacing: HygurSpacing.xs) {
                Text(item.title)
                    .lineLimit(1)

                HStack(spacing: HygurSpacing.sm) {
                    BadgeView(
                        text: item.sourceType.uppercased(),
                        color: HygurColors.sourceTypeColor(item.sourceType),
                        style: .rounded
                    )

                    Text("\(item.chunkCount) chunks")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)

                    // Project badge
                    if let projectName = projectName {
                        BadgeView(
                            text: projectName,
                            color: .purple,
                            style: .rounded,
                            icon: "folder.fill"
                        )
                    }

                    // Tags (limit to maxVisibleTags, show +N for more)
                    if !item.tags.isEmpty {
                        tagsView
                    }
                }
            }

            Spacer()

            Text(item.documentDate, style: .date)
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textSecondary)
        }
        .padding(.vertical, HygurSpacing.xs)
    }

    /// Leading visual for a row: thumbnail for images, waveform icon for audio,
    /// standard SF symbol for all other source types.
    @ViewBuilder
    private var sourceTypeLeadingView: some View {
        if item.sourceType == "image" {
            if let path = item.sourcePath, let nsImage = NSImage(contentsOfFile: path) {
                Image(nsImage: nsImage)
                    .resizable()
                    .scaledToFill()
                    .frame(width: 40, height: 40)
                    .clipShape(RoundedRectangle(cornerRadius: 6))
            } else {
                Image(systemName: "photo")
                    .foregroundStyle(HygurColors.sourceTypeColor(item.sourceType))
                    .font(.system(size: 20))
            }
        } else if item.sourceType == "audio" {
            Image(systemName: "waveform")
                .foregroundStyle(HygurColors.sourceTypeColor(item.sourceType))
                .font(.system(size: 20))
        } else {
            Image(systemName: HygurColors.sourceTypeIcon(item.sourceType))
                .foregroundStyle(HygurColors.sourceTypeColor(item.sourceType))
                .font(.system(size: 16))
        }
    }

    @ViewBuilder
    private var tagsView: some View {
        let visibleTagSummaries = Array(item.tags.prefix(maxVisibleTags))
        let visibleTags = visibleTagSummaries.map { Tag(id: $0.id, name: $0.name, color: $0.color, usageCount: 0) }
        let remainingCount = item.tags.count - maxVisibleTags

        HStack(spacing: 4) {
            ForEach(visibleTags) { tag in
                TagPillView(tag: tag)
            }

            if remainingCount > 0 {
                Text("+\(remainingCount)")
                    .font(.caption2)
                    .fontWeight(.medium)
                    .padding(.horizontal, 6)
                    .padding(.vertical, 4)
                    .background(Color.secondary.opacity(0.15))
                    .foregroundStyle(.secondary)
                    .clipShape(Capsule())
            }
        }
    }

}

// MARK: - Preview

#Preview {
    KnowledgeBaseView()
        .frame(width: 600, height: 400)
}
