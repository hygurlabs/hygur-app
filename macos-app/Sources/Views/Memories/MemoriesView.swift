import SwiftUI

/// Lists every persistent memory the sidecar has stored. Lets the user audit
/// what was auto-extracted from past chats and prune anything off-base.
///
/// Phase 3.3: pending candidates (auto-extracted, not yet reviewed) surface
/// in a dedicated "Pending review" section above the accepted list. Until
/// the user clicks "Accept", the chat handler's `SearchAccepted` skips the
/// row — so the LLM never sees an unreviewed extraction.
struct MemoriesView: View {
    @State private var viewModel = MemoriesViewModel()
    @State private var searchText: String = ""
    @State private var pendingDeletion: MemoryItem?

    var body: some View {
        VStack(spacing: 0) {
            FeatureHeader(title: "Memories", count: viewModel.memories.count) {
                Button {
                    Task { await viewModel.load() }
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .help("Reload")
                .buttonStyle(.plain)
            }

            Divider()

            if viewModel.isLoading && viewModel.memories.isEmpty {
                ProgressView()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if filteredPending.isEmpty && filteredGroups.isEmpty {
                emptyState
            } else {
                memoryList
            }
        }
        .toolbar {
            ToolbarItem(placement: .navigation) {
                ToolbarSearchField(text: $searchText, prompt: "Search memories")
            }
        }
        .task { await viewModel.load() }
        .alert(item: $pendingDeletion) { memory in
            Alert(
                title: Text("Delete this memory?"),
                message: Text(memory.content),
                primaryButton: .destructive(Text("Delete")) {
                    Task { await viewModel.delete(memory) }
                },
                secondaryButton: .cancel()
            )
        }
    }

    private var filteredPending: [MemoryItem] {
        guard !searchText.isEmpty else { return viewModel.pendingMemories }
        let query = searchText.lowercased()
        return viewModel.pendingMemories.filter { $0.content.lowercased().contains(query) }
    }

    private var filteredGroups: [(MemoryKind, [MemoryItem])] {
        guard !searchText.isEmpty else { return viewModel.groupedByKind }
        let query = searchText.lowercased()
        return viewModel.groupedByKind.compactMap { kind, items in
            let matches = items.filter { $0.content.lowercased().contains(query) }
            return matches.isEmpty ? nil : (kind, matches)
        }
    }

    private var emptyState: some View {
        Group {
            if searchText.isEmpty {
                EmptyStateView(
                    icon: "brain.head.profile",
                    title: "No memories yet",
                    subtitle: "Hygur automatically extracts durable facts (preferences, identities, deadlines) from your conversations. After each session you'll see suggestions here for review."
                )
            } else {
                EmptyStateView(
                    icon: "magnifyingglass",
                    title: "Nothing matches “\(searchText)”"
                )
            }
        }
    }

    private var memoryList: some View {
        List {
            if !filteredPending.isEmpty {
                Section {
                    ForEach(filteredPending) { memory in
                        PendingMemoryRow(
                            memory: memory,
                            onAccept: { Task { await viewModel.accept(memory) } },
                            onDiscard: { Task { await viewModel.discard(memory) } }
                        )
                    }
                } header: {
                    HStack(spacing: HygurSpacing.xs) {
                        Image(systemName: "sparkles")
                            .foregroundStyle(HygurColors.warning)
                        Text("Pending review")
                            .fontWeight(.semibold)
                        Text("\(filteredPending.count)")
                            .font(HygurTypography.captionMono)
                            .foregroundStyle(HygurColors.warning)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 1)
                            .background(HygurColors.warning.opacity(0.15))
                            .clipShape(Capsule())
                    }
                }
            }

            ForEach(filteredGroups, id: \.0) { kind, items in
                Section(kind.label) {
                    ForEach(items) { memory in
                        MemoryRow(memory: memory) {
                            pendingDeletion = memory
                        }
                    }
                }
            }
        }
        .listStyle(.inset)
    }
}

// MARK: - Pending Row

/// Surfaces an auto-extracted candidate awaiting review. Provides explicit
/// Accept (promotes to the main list, eligible for chat injection) and
/// Discard (sidecar deletes outright) actions. Discard is destructive but
/// no confirmation alert because this row was *suggested*, not user-created.
private struct PendingMemoryRow: View {
    let memory: MemoryItem
    let onAccept: () -> Void
    let onDiscard: () -> Void

    @State private var isHovered = false

    var body: some View {
        HStack(alignment: .top, spacing: HygurSpacing.sm) {
            kindBadge(MemoryKind(raw: memory.type))

            VStack(alignment: .leading, spacing: HygurSpacing.xxs) {
                Text(memory.content)
                    .font(.body)

                if let date = formattedDate(memory.createdAt) {
                    Text("Suggested \(date)")
                        .font(.caption2)
                        .foregroundStyle(.tertiary)
                }
            }

            Spacer()

            HStack(spacing: HygurSpacing.xs) {
                Button {
                    onDiscard()
                } label: {
                    Label("Discard", systemImage: "xmark")
                        .labelStyle(.iconOnly)
                }
                .buttonStyle(.plain)
                .foregroundStyle(HygurColors.danger)
                .help("Discard suggestion")

                Button {
                    onAccept()
                } label: {
                    Label("Accept", systemImage: "checkmark")
                        .font(.caption)
                        .fontWeight(.medium)
                        .padding(.horizontal, HygurSpacing.sm)
                        .padding(.vertical, 4)
                        .background(HygurColors.accent.opacity(0.18), in: Capsule())
                        .foregroundStyle(HygurColors.accent)
                }
                .buttonStyle(.plain)
                .help("Accept and add to memories")
            }
        }
        .padding(.vertical, HygurSpacing.xs)
        .contentShape(Rectangle())
        .onHover { isHovered = $0 }
        .background(HygurColors.warning.opacity(isHovered ? 0.06 : 0))
        .contextMenu {
            Button { onAccept() } label: {
                Label("Accept", systemImage: "checkmark")
            }
            Button(role: .destructive) { onDiscard() } label: {
                Label("Discard", systemImage: "trash")
            }
        }
    }

    private func kindBadge(_ kind: MemoryKind) -> some View {
        Text(badgeLabel(for: kind))
            .font(.caption2)
            .fontWeight(.medium)
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(badgeColor(for: kind).opacity(0.18), in: Capsule())
            .foregroundStyle(badgeColor(for: kind))
    }

    private func badgeLabel(for kind: MemoryKind) -> String {
        switch kind {
        case .fact:       return "Fact"
        case .preference: return "Pref"
        case .action:     return "Action"
        }
    }

    private func badgeColor(for kind: MemoryKind) -> Color {
        switch kind {
        case .fact:       return .blue
        case .preference: return .purple
        case .action:     return .orange
        }
    }

    private func formattedDate(_ raw: String) -> String? {
        let iso = ISO8601DateFormatter()
        iso.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let date = iso.date(from: raw) ?? ISO8601DateFormatter().date(from: raw)
        guard let date else { return nil }
        return date.formatted(.relative(presentation: .named))
    }
}

// MARK: - Accepted Row

private struct MemoryRow: View {
    let memory: MemoryItem
    let onDelete: () -> Void

    @State private var isHovered = false

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            kindBadge(MemoryKind(raw: memory.type))

            VStack(alignment: .leading, spacing: 2) {
                Text(memory.content)
                    .font(.body)

                HStack(spacing: HygurSpacing.xs) {
                    if let date = formattedDate(memory.createdAt) {
                        Text(date)
                            .font(.caption2)
                            .foregroundStyle(.tertiary)
                    }
                    if memory.isExtracted {
                        Text("• auto")
                            .font(.caption2)
                            .foregroundStyle(.tertiary)
                            .help("Extracted from a chat session and accepted by you.")
                    }
                }
            }

            Spacer()

            if isHovered {
                Button(role: .destructive, action: onDelete) {
                    Image(systemName: "trash")
                        .foregroundStyle(.red)
                }
                .buttonStyle(.plain)
                .help("Delete")
            }
        }
        .padding(.vertical, 4)
        .contentShape(Rectangle())
        .onHover { isHovered = $0 }
        .contextMenu {
            Button(role: .destructive, action: onDelete) {
                Label("Delete", systemImage: "trash")
            }
        }
    }

    private func kindBadge(_ kind: MemoryKind) -> some View {
        Text(badgeLabel(for: kind))
            .font(.caption2)
            .fontWeight(.medium)
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(badgeColor(for: kind).opacity(0.18), in: Capsule())
            .foregroundStyle(badgeColor(for: kind))
    }

    private func badgeLabel(for kind: MemoryKind) -> String {
        switch kind {
        case .fact:       return "Fact"
        case .preference: return "Pref"
        case .action:     return "Action"
        }
    }

    private func badgeColor(for kind: MemoryKind) -> Color {
        switch kind {
        case .fact:       return .blue
        case .preference: return .purple
        case .action:     return .orange
        }
    }

    private func formattedDate(_ raw: String) -> String? {
        let iso = ISO8601DateFormatter()
        iso.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let date = iso.date(from: raw) ?? ISO8601DateFormatter().date(from: raw)
        guard let date else { return nil }
        return date.formatted(.relative(presentation: .named))
    }
}

#Preview {
    MemoriesView()
}
