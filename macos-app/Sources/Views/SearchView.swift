import SwiftUI

struct SearchView: View {
    @StateObject private var viewModel = SearchViewModel()
    @State private var showPreview = false

    private let dateFormatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.dateFormat = "yyyy-MM-dd"
        return formatter
    }()

    var body: some View {
        VStack(spacing: 0) {
            searchBar
            Divider()
            if showPreview && !viewModel.query.isEmpty {
                previewSection
                Divider()
            }
            if viewModel.isSearching {
                loadingState
            } else if let errorMessage = viewModel.error {
                errorState(message: errorMessage)
            } else if viewModel.results.isEmpty {
                if viewModel.query.isEmpty {
                    emptyState
                } else {
                    noResultsState
                }
            } else {
                resultsList
            }
        }
        .onChange(of: viewModel.query) { _, _ in
            viewModel.searchDebounced()
        }
        .onChange(of: viewModel.dateFrom) { _, _ in
            if !viewModel.query.isEmpty { Task { await viewModel.search() } }
        }
        .onChange(of: viewModel.dateTo) { _, _ in
            if !viewModel.query.isEmpty { Task { await viewModel.search() } }
        }
        .onChange(of: viewModel.projectFilterId) { _, _ in
            if !viewModel.query.isEmpty { Task { await viewModel.search() } }
        }
        .task {
            await viewModel.loadProjectsIfNeeded()
        }
    }

    private var searchBar: some View {
        VStack(spacing: 8) {
            HStack(spacing: 8) {
                Image(systemName: "magnifyingglass")
                    .foregroundStyle(.secondary)

                TextField("Search knowledge base...", text: $viewModel.query)
                    .textFieldStyle(.plain)

                if !viewModel.query.isEmpty {
                    Button {
                        viewModel.clearSearch()
                    } label: {
                        Image(systemName: "xmark.circle.fill")
                            .foregroundStyle(.secondary)
                    }
                    .buttonStyle(.plain)
                }

                Button {
                    showPreview.toggle()
                } label: {
                    Image(systemName: "leaf")
                        .foregroundStyle(Color.accentColor)
                }
                .buttonStyle(.plain)
                .help("Toggle query preview")
            }

            HStack(spacing: 12) {
                projectFilterMenu

                VStack(alignment: .leading, spacing: 2) {
                    Text("From")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                    DatePicker(
                        "",
                        selection: Binding<Date>(
                            get: { viewModel.dateFrom ?? Date() },
                            set: { viewModel.dateFrom = $0 }
                        ),
                        displayedComponents: .date
                    )
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .fixedSize(horizontal: false, vertical: true)
                }

                VStack(alignment: .leading, spacing: 2) {
                    Text("To")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                    DatePicker(
                        "",
                        selection: Binding<Date>(
                            get: { viewModel.dateTo ?? Date() },
                            set: { viewModel.dateTo = $0 }
                        ),
                        displayedComponents: .date
                    )
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .fixedSize(horizontal: false, vertical: true)
                }

                Spacer()
            }
        }
        .padding()
    }

    /// Inline menu that lets the user restrict raw search to a single project,
    /// the SearchView equivalent of the Mode Focus pill in ChatView. Picking
    /// "Tous les projets" clears the filter.
    private var projectFilterMenu: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text("Projet")
                .font(.caption2)
                .foregroundStyle(.secondary)
            Menu {
                Button("Tous les projets") { viewModel.projectFilterId = nil }
                if !viewModel.availableProjects.isEmpty {
                    Divider()
                    ForEach(viewModel.availableProjects) { project in
                        Button(project.name) { viewModel.projectFilterId = project.id }
                    }
                }
            } label: {
                HStack(spacing: 4) {
                    Image(systemName: "scope")
                    Text(activeProjectLabel)
                        .lineLimit(1)
                }
                .frame(minWidth: 120, alignment: .leading)
            }
            .menuStyle(.borderlessButton)
            .fixedSize(horizontal: true, vertical: true)
        }
    }

    private var activeProjectLabel: String {
        guard let id = viewModel.projectFilterId,
              let project = viewModel.availableProjects.first(where: { $0.id == id }) else {
            return "Tous"
        }
        return project.name
    }

    private var previewSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text("Query Preview")
                    .font(.headline)
                Spacer()
                Button {
                    showPreview = false
                } label: {
                    Image(systemName: "xmark")
                        .foregroundStyle(.secondary)
                }
                .buttonStyle(.plain)
            }

            VStack(alignment: .leading, spacing: 2) {
                Text("Query")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                Text(viewModel.query)
                    .font(.body)
                    .textSelection(.enabled)
            }

            VStack(alignment: .leading, spacing: 2) {
                Text("Parameters")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                if let dateFrom = viewModel.dateFrom {
                    ParameterRow(label: "From", value: dateFormatter.string(from: dateFrom))
                }
                if let dateTo = viewModel.dateTo {
                    ParameterRow(label: "To", value: dateFormatter.string(from: dateTo))
                }
                ParameterRow(label: "Top K", value: "20")
                ParameterRow(label: "Mode", value: "Semantic")
            }
            .padding(8)
            .background(
                RoundedRectangle(cornerRadius: 6)
                    .fill(HygurColors.surface)
            )
        }
        .padding(.horizontal)
        .padding(.vertical, 8)
    }

    private var emptyState: some View {
        EmptyStateView(
            icon: "magnifyingglass",
            title: "Search your knowledge base",
            subtitle: "Enter a query above to find relevant documents"
        )
    }

    private var noResultsState: some View {
        EmptyStateView(
            icon: "doc.text.magnifyingglass",
            title: "No results",
            subtitle: "No results for \"\(viewModel.query)\""
        )
    }

    private func errorState(message: String) -> some View {
        EmptyStateView(
            icon: "exclamationmark.triangle",
            title: "Search failed",
            subtitle: message,
            action: ("Retry", { Task { await viewModel.search() } })
        )
    }

    private var loadingState: some View {
        VStack(spacing: HygurSpacing.md) {
            LoadingIndicator(style: .large)
            Text("Searching...")
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textSecondary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private var resultsList: some View {
        List(viewModel.results) { result in
            SearchResultRow(result: result, query: viewModel.query)
        }
    }
}

struct SearchResultRow: View {
    let result: SearchResult
    let query: String

    private let dateFormatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.dateStyle = .medium
        formatter.timeStyle = .none
        return formatter
    }()

    var body: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm - 2) {
            HStack {
                Text(result.title)
                    .font(HygurTypography.headline)
                    .lineLimit(1)
                Spacer()
                if let date = result.date {
                    Text(dateFormatter.string(from: date))
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                }
            }

            highlightedExcerpt
                .font(HygurTypography.subheadline)
                .foregroundStyle(HygurColors.textSecondary)
                .lineLimit(2)

            HStack(spacing: HygurSpacing.md) {
                BadgeView(text: "Semantic", color: .purple, style: .rounded)
                if result.sourceType == "mail" {
                    BadgeView(text: "Email", color: .blue, style: .rounded)
                }
                Spacer()
                Text(String(format: "%.1f%%", result.score * 100))
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textSecondary)
            }
        }
        .padding(.vertical, HygurSpacing.sm)
    }

    private var highlightedExcerpt: some View {
        let excerpt = result.excerpt
        let queryLower = query.lowercased()

        if let range = excerpt.lowercased().range(of: queryLower) {
            let before = String(excerpt[..<range.lowerBound])
            let match = String(excerpt[range])
            let after = String(excerpt[range.upperBound...])
            return Text("\(before) \(match) \(after)")
        }
        return Text(excerpt)
    }
}

struct ParameterRow: View {
    let label: String
    let value: String

    var body: some View {
        HStack {
            Text(label)
                .font(.caption)
                .foregroundStyle(.secondary)
            Spacer()
            Text(value)
                .font(.caption)
        }
    }
}

#Preview {
    SearchView()
        .frame(width: 600, height: 500)
}
