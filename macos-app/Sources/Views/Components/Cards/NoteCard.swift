import SwiftUI

/// `HygurCard`-based row for the Notes list. Replaces the inline `NoteRow`
/// styling with the unified card primitive so notes match other entity
/// surfaces.
struct NoteCard: View {
    let note: Note
    var fillContainer: Bool = false
    @Environment(FavoritesStore.self) private var favorites

    var body: some View {
        HygurCard(
            icon: "note.text",
            iconTint: HygurColors.brandGold,
            title: note.title,
            subtitle: formattedDate(note.updatedAt),
            fillContainer: fillContainer,
            accessory: {
                Button {
                    favorites.toggleNote(note.id)
                } label: {
                    let isFav = favorites.isFavorite(noteId: note.id)
                    Image(systemName: isFav ? "star.fill" : "star")
                        .foregroundStyle(isFav ? HygurColors.brandGold : HygurColors.textTertiary)
                        .font(.system(size: 13, weight: .medium))
                }
                .buttonStyle(.plain)
                .help(favorites.isFavorite(noteId: note.id) ? "Remove from favorites" : "Add to favorites")
            },
            content: {
                Text(note.content)
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textSecondary)
                    .lineLimit(2)
                    .frame(maxWidth: .infinity, alignment: .leading)
            },
            footer: {
                if !note.tags.isEmpty {
                    HStack(spacing: HygurSpacing.xs) {
                        ForEach(note.tags.prefix(3)) { tag in
                            TagPillView(tag: tag)
                        }
                        if note.tags.count > 3 {
                            Text("+\(note.tags.count - 3)")
                                .font(HygurTypography.caption)
                                .foregroundStyle(HygurColors.textSecondary)
                        }
                        Spacer()
                    }
                }
            }
        )
    }

    private func formattedDate(_ date: Date) -> String {
        let formatter = RelativeDateTimeFormatter()
        formatter.unitsStyle = .abbreviated
        return formatter.localizedString(for: date, relativeTo: Date())
    }
}
