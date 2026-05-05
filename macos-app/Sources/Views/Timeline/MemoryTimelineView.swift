import SwiftUI

/// Replaces the old force-directed Graph view. The user types a topic and
/// gets a chronological frieze of grouped events ("chapters") instead of a
/// flat list. Tapping a chapter expands it to reveal the underlying events.
struct MemoryTimelineView: View {
    @State private var viewModel = TimelineViewModel()
    @State private var quickLookContentId: IdentifiableString?
    @State private var selectedEventID: String?

    var body: some View {
        VStack(spacing: 0) {
            FeatureHeader(title: "Timeline", count: viewModel.totalEvents) {
                if viewModel.isLoading {
                    ProgressView().controlSize(.small)
                }
            }

            searchBar
                .padding(.horizontal, HygurSpacing.lg)
                .padding(.bottom, HygurSpacing.sm)

            Divider()

            content
        }
        .sheet(item: $quickLookContentId) { wrapper in
            DocumentQuickLookSheet(contentId: wrapper.value)
        }
    }

    // MARK: - Search

    private var searchBar: some View {
        HStack(spacing: HygurSpacing.sm) {
            Image(systemName: "magnifyingglass")
                .foregroundStyle(HygurColors.textSecondary)

            TextField(
                "Tape un sujet : TVA, projet X, personne…",
                text: Binding(
                    get: { viewModel.query },
                    set: { newValue in
                        viewModel.query = newValue
                        viewModel.searchDebounced()
                    }
                )
            )
            .textFieldStyle(.plain)
            .font(HygurTypography.body)
            .onSubmit { viewModel.searchNow() }

            if !viewModel.query.isEmpty {
                Button {
                    viewModel.query = ""
                    viewModel.searchDebounced()
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .foregroundStyle(HygurColors.textTertiary)
                }
                .buttonStyle(.plain)
                .help("Effacer")
            }
        }
        .padding(.horizontal, HygurSpacing.md)
        .padding(.vertical, HygurSpacing.sm)
        .background(Color.secondary.opacity(0.08), in: RoundedRectangle(cornerRadius: HygurRadius.md))
    }

    // MARK: - Content

    @ViewBuilder
    private var content: some View {
        if let error = viewModel.error {
            EmptyStateView(
                icon: "exclamationmark.triangle",
                title: "Erreur",
                subtitle: error
            )
        } else if viewModel.query.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            EmptyStateView(
                icon: "clock.arrow.circlepath",
                title: "Explore ta mémoire",
                subtitle: "Tape un sujet, un projet, une personne. Hygur t'affiche une frise chronologique des événements liés."
            )
        } else if viewModel.chapters.isEmpty && !viewModel.isLoading {
            EmptyStateView(
                icon: "magnifyingglass",
                title: "Aucun événement",
                subtitle: "Rien ne correspond à « \(viewModel.query) » dans la fenêtre de \(viewModel.rangeDays) jours."
            )
        } else {
            timeline
        }
    }

    private var timeline: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: HygurSpacing.md) {
                ForEach(viewModel.chapters) { chapter in
                    ChapterCard(
                        chapter: chapter,
                        isExpanded: viewModel.expandedChapterID == chapter.id,
                        selectedEventID: selectedEventID,
                        onToggle: { viewModel.toggle(chapter) },
                        onEntityTap: { entity in
                            viewModel.query = entity
                            viewModel.searchNow()
                        },
                        onEventSelect: { event in
                            selectedEventID = event.id
                        },
                        onEventOpen: { event in
                            quickLookContentId = IdentifiableString(event.contentId)
                        }
                    )
                }
            }
            .padding(HygurSpacing.lg)
        }
        // Space bar opens QuickLook for the selected event.
        .focusable()
        .onKeyPress(.space) {
            guard let selectedID = selectedEventID else { return .ignored }
            for chapter in viewModel.chapters {
                if let event = chapter.events.first(where: { $0.id == selectedID }) {
                    quickLookContentId = IdentifiableString(event.contentId)
                    return .handled
                }
            }
            return .ignored
        }
    }
}

// MARK: - Chapter Card

private struct ChapterCard: View {
    let chapter: TimelineChapter
    let isExpanded: Bool
    let selectedEventID: String?
    let onToggle: () -> Void
    let onEntityTap: (String) -> Void
    let onEventSelect: (TimelineEvent) -> Void
    let onEventOpen: (TimelineEvent) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
                .contentShape(Rectangle())
                .onTapGesture { withAnimation(.easeInOut(duration: 0.18)) { onToggle() } }

            if isExpanded {
                Divider().padding(.vertical, HygurSpacing.sm)
                eventsList
                    .transition(.opacity.combined(with: .move(edge: .top)))
            }
        }
        .padding(HygurSpacing.md)
        .background(HygurColors.surface, in: RoundedRectangle(cornerRadius: HygurRadius.md))
        .overlay(
            RoundedRectangle(cornerRadius: HygurRadius.md)
                .stroke(HygurColors.border, lineWidth: 1)
        )
    }

    // MARK: Header

    private var header: some View {
        HStack(alignment: .top, spacing: HygurSpacing.md) {
            VStack(alignment: .leading, spacing: 4) {
                Text(rangeLabel)
                    .font(HygurTypography.captionMono)
                    .foregroundStyle(HygurColors.textTertiary)

                Text(chapter.title)
                    .font(HygurTypography.headline)
                    .foregroundStyle(HygurColors.textPrimary)

                if !chapter.dominantEntities.isEmpty {
                    entitiesRow
                }
            }

            Spacer()

            HStack(spacing: HygurSpacing.xs) {
                Text("\(chapter.eventCount)")
                    .font(HygurTypography.captionMono)
                    .foregroundStyle(HygurColors.textSecondary)
                Image(systemName: isExpanded ? "chevron.up" : "chevron.down")
                    .font(.caption)
                    .foregroundStyle(HygurColors.textSecondary)
            }
            .padding(.horizontal, HygurSpacing.sm)
            .padding(.vertical, 3)
            .background(Color.secondary.opacity(0.12), in: Capsule())
        }
    }

    private var entitiesRow: some View {
        HStack(spacing: HygurSpacing.xs) {
            ForEach(chapter.dominantEntities.prefix(4), id: \.self) { entity in
                Button {
                    onEntityTap(entity)
                } label: {
                    Text(entity)
                        .font(HygurTypography.caption)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .background(HygurColors.accent.opacity(0.12), in: Capsule())
                        .foregroundStyle(HygurColors.accent)
                }
                .buttonStyle(.plain)
                .help("Recentrer la timeline sur \(entity)")
            }
        }
    }

    private var rangeLabel: String {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "fr_FR")
        formatter.dateFormat = "dd MMM yyyy"
        let start = formatter.string(from: chapter.parsedStart)
        let end = formatter.string(from: chapter.parsedEnd)
        return start == end ? start : "\(start) → \(end)"
    }

    // MARK: Events

    private var eventsList: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.xs) {
            ForEach(chapter.events) { event in
                EventRow(
                    event: event,
                    isSelected: selectedEventID == event.id,
                    onSelect: { onEventSelect(event) },
                    onOpen: { onEventOpen(event) }
                )
            }
        }
    }
}

// MARK: - Event Row

private struct EventRow: View {
    let event: TimelineEvent
    let isSelected: Bool
    let onSelect: () -> Void
    let onOpen: () -> Void

    var body: some View {
        HStack(alignment: .top, spacing: HygurSpacing.sm) {
            // Timeline dot + date
            VStack(alignment: .leading, spacing: 2) {
                Text(formattedDate)
                    .font(HygurTypography.captionMono)
                    .foregroundStyle(HygurColors.textTertiary)
                    .frame(width: 90, alignment: .leading)
            }

            // Connector dot
            Circle()
                .fill(sourceColor)
                .frame(width: 6, height: 6)
                .padding(.top, 6)

            VStack(alignment: .leading, spacing: 2) {
                if let title = event.title, !title.isEmpty {
                    Text(title)
                        .font(HygurTypography.body)
                        .foregroundStyle(HygurColors.textPrimary)
                        .lineLimit(1)
                }

                if let snippet = event.snippet, !snippet.isEmpty {
                    Text(snippet)
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                        .lineLimit(2)
                }

                if let context = event.context, !context.isEmpty, context != event.snippet {
                    Text(context)
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textTertiary)
                        .lineLimit(1)
                }
            }

            Spacer()
        }
        .padding(.vertical, HygurSpacing.xs)
        .padding(.horizontal, HygurSpacing.sm)
        .background(
            isSelected
                ? HygurColors.accent.opacity(0.10)
                : Color.clear,
            in: RoundedRectangle(cornerRadius: HygurRadius.sm)
        )
        .contentShape(Rectangle())
        // Double click → open QuickLook immediately.
        .onTapGesture(count: 2) { onOpen() }
        // Single click → select (then Space to open).
        .onTapGesture(count: 1) { onSelect() }
        .help("Double-cliquer ou appuyer sur Espace pour ouvrir")
    }

    private var sourceColor: Color {
        HygurColors.sourceTypeColor(event.sourceType ?? "")
    }

    private var formattedDate: String {
        let df = DateFormatter()
        df.locale = Locale(identifier: "fr_FR")
        df.dateFormat = "yyyy-MM-dd"
        guard let date = df.date(from: event.date) else { return event.date }
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "fr_FR")
        formatter.dateFormat = "dd MMM yyyy"
        return formatter.string(from: date)
    }
}

#Preview {
    MemoryTimelineView()
        .frame(width: 800, height: 600)
}
