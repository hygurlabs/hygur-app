import SwiftUI

/// Lists every persistent memory the sidecar has stored. Lets the user audit
/// what was auto-extracted from past chats and prune anything off-base.
struct MemoriesView: View {
    @State private var viewModel = MemoriesViewModel()
    @State private var searchText: String = ""
    @State private var pendingDeletion: MemoryItem?

    var body: some View {
        VStack(spacing: 0) {
            FeatureHeader(title: "Mémoires", count: viewModel.memories.count) {
                Button {
                    Task { await viewModel.load() }
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .help("Recharger")
                .buttonStyle(.plain)
            }

            Divider()

            if viewModel.isLoading && viewModel.memories.isEmpty {
                ProgressView()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if filteredGroups.isEmpty {
                emptyState
            } else {
                memoryList
            }
        }
        .searchable(text: $searchText, prompt: "Rechercher dans les mémoires…")
        .task { await viewModel.load() }
        .alert(item: $pendingDeletion) { memory in
            Alert(
                title: Text("Supprimer cette mémoire ?"),
                message: Text(memory.content),
                primaryButton: .destructive(Text("Supprimer")) {
                    Task { await viewModel.delete(memory) }
                },
                secondaryButton: .cancel()
            )
        }
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
                    title: "Aucune mémoire pour l'instant",
                    subtitle: "Hygur extrait automatiquement les faits durables (préférences, identités, deadlines) à partir de tes conversations."
                )
            } else {
                EmptyStateView(
                    icon: "magnifyingglass",
                    title: "Rien ne correspond à « \(searchText) »"
                )
            }
        }
    }

    private var memoryList: some View {
        List {
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

// MARK: - Row

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

                if let date = formattedDate(memory.createdAt) {
                    Text(date)
                        .font(.caption2)
                        .foregroundStyle(.tertiary)
                }
            }

            Spacer()

            if isHovered {
                Button(role: .destructive, action: onDelete) {
                    Image(systemName: "trash")
                        .foregroundStyle(.red)
                }
                .buttonStyle(.plain)
                .help("Supprimer")
            }
        }
        .padding(.vertical, 4)
        .contentShape(Rectangle())
        .onHover { isHovered = $0 }
        .contextMenu {
            Button(role: .destructive, action: onDelete) {
                Label("Supprimer", systemImage: "trash")
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
        case .fact:       return "Fait"
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
