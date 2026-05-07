import SwiftUI

/// Right-rail inspector for a note. Surfaces tags, timestamps, project link,
/// and the favorite toggle. Loads the note lazily because the panel only
/// receives the id from the sidebar selection.
struct NotePropertiesView: View {
    let noteId: String

    @Environment(FavoritesStore.self) private var favorites
    @State private var note: Note?
    @State private var loadError: String?

    private let sidecar = SidecarService.fromSettings()

    var body: some View {
        Group {
            if let note {
                content(for: note)
            } else if let loadError {
                Text(loadError)
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.danger)
            } else {
                ProgressView()
                    .controlSize(.small)
            }
        }
        .task(id: noteId) { await load() }
    }

    @ViewBuilder
    private func content(for note: Note) -> some View {
        VStack(alignment: .leading, spacing: HygurSpacing.md) {
            row(label: "Title", value: note.title)

            HStack {
                Text("Favorite")
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textSecondary)
                Spacer()
                Button {
                    favorites.toggleNote(note.id)
                } label: {
                    let isFav = favorites.isFavorite(noteId: note.id)
                    Image(systemName: isFav ? "star.fill" : "star")
                        .foregroundStyle(isFav ? HygurColors.brandGold : HygurColors.textTertiary)
                }
                .buttonStyle(.plain)
            }

            row(label: "Updated", value: note.updatedAt.formatted(date: .abbreviated, time: .shortened))
            row(label: "Created", value: note.createdAt.formatted(date: .abbreviated, time: .shortened))

            if let projectId = note.projectId, !projectId.isEmpty {
                row(label: "Project", value: projectId)
            }

            if !note.tags.isEmpty {
                VStack(alignment: .leading, spacing: HygurSpacing.xs) {
                    Text("Tags")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                    NotePropertiesFlow(spacing: HygurSpacing.xs) {
                        ForEach(note.tags) { tag in
                            TagPillView(tag: tag)
                        }
                    }
                }
            }

            Spacer()
        }
    }

    @ViewBuilder
    private func row(label: String, value: String) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(label)
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textSecondary)
            Text(value)
                .font(HygurTypography.cardMeta)
                .foregroundStyle(HygurColors.textPrimary)
                .textSelection(.enabled)
        }
    }

    private func load() async {
        do {
            note = try await sidecar.getNote(id: noteId)
        } catch {
            loadError = error.localizedDescription
        }
    }
}

/// Minimal flow layout — wraps tag pills onto multiple lines without us
/// pulling in a layout engine. Generated rows use the natural widths of
/// each child so we don't have to measure them manually.
private struct NotePropertiesFlow: Layout {
    var spacing: CGFloat = 4

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let width = proposal.width ?? .infinity
        return computeRows(width: width, subviews: subviews).size
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        let rows = computeRows(width: bounds.width, subviews: subviews)
        for placement in rows.placements {
            placement.subview.place(at: CGPoint(x: bounds.minX + placement.point.x,
                                                y: bounds.minY + placement.point.y),
                                    proposal: .unspecified)
        }
    }

    private struct Placement {
        let subview: LayoutSubview
        let point: CGPoint
    }

    private struct Result {
        let size: CGSize
        let placements: [Placement]
    }

    private func computeRows(width: CGFloat, subviews: Subviews) -> Result {
        var placements: [Placement] = []
        var x: CGFloat = 0
        var y: CGFloat = 0
        var rowHeight: CGFloat = 0
        var maxWidth: CGFloat = 0

        for sv in subviews {
            let size = sv.sizeThatFits(.unspecified)
            if x + size.width > width && x > 0 {
                x = 0
                y += rowHeight + spacing
                rowHeight = 0
            }
            placements.append(Placement(subview: sv, point: CGPoint(x: x, y: y)))
            x += size.width + spacing
            rowHeight = max(rowHeight, size.height)
            maxWidth = max(maxWidth, x)
        }
        return Result(size: CGSize(width: maxWidth, height: y + rowHeight), placements: placements)
    }
}
