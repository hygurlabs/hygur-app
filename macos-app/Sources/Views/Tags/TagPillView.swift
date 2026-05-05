import SwiftUI

/// A reusable colored tag pill component.
/// Used in lists, note creation, and knowledge items.
struct TagPillView: View {
    let tag: Tag
    var showRemoveButton: Bool = false
    var onRemove: (() -> Void)?

    var body: some View {
        HStack(spacing: HygurSpacing.xs) {
            Text(tag.name)
                .font(HygurTypography.caption)
                .fontWeight(.medium)

            if showRemoveButton {
                Button {
                    onRemove?()
                } label: {
                    Image(systemName: "xmark")
                        .font(.system(size: 8, weight: .bold))
                }
                .buttonStyle(.plain)
                .opacity(0.7)
            }
        }
        .padding(.horizontal, HygurSpacing.sm)
        .padding(.vertical, HygurSpacing.xs)
        .background(tag.swiftUIColor.opacity(0.2))
        .foregroundStyle(tag.swiftUIColor)
        .clipShape(Capsule())
        .overlay(
            Capsule()
                .strokeBorder(tag.swiftUIColor.opacity(0.3), lineWidth: 1)
        )
    }
}

/// A simple colored tag pill using just name and color.
/// Useful when a full Tag object is not available.
struct SimpleTagPillView: View {
    let name: String
    let color: Color
    var showRemoveButton: Bool = false
    var onRemove: (() -> Void)?

    var body: some View {
        HStack(spacing: HygurSpacing.xs) {
            Text(name)
                .font(HygurTypography.caption)
                .fontWeight(.medium)

            if showRemoveButton {
                Button {
                    onRemove?()
                } label: {
                    Image(systemName: "xmark")
                        .font(.system(size: 8, weight: .bold))
                }
                .buttonStyle(.plain)
                .opacity(0.7)
            }
        }
        .padding(.horizontal, HygurSpacing.sm)
        .padding(.vertical, HygurSpacing.xs)
        .background(color.opacity(0.2))
        .foregroundStyle(color)
        .clipShape(Capsule())
        .overlay(
            Capsule()
                .strokeBorder(color.opacity(0.3), lineWidth: 1)
        )
    }
}

/// Tag pill with selection state for pickers.
struct SelectableTagPillView: View {
    let tag: Tag
    let isSelected: Bool
    var onTap: (() -> Void)?

    var body: some View {
        Button {
            onTap?()
        } label: {
            HStack(spacing: HygurSpacing.xs) {
                if isSelected {
                    Image(systemName: "checkmark")
                        .font(.system(size: 10, weight: .bold))
                }
                Text(tag.name)
                    .font(HygurTypography.caption)
                    .fontWeight(.medium)
            }
            .padding(.horizontal, HygurSpacing.sm)
            .padding(.vertical, HygurSpacing.xs)
            .background(isSelected ? tag.swiftUIColor : tag.swiftUIColor.opacity(0.2))
            .foregroundStyle(isSelected ? .white : tag.swiftUIColor)
            .clipShape(Capsule())
            .overlay(
                Capsule()
                    .strokeBorder(tag.swiftUIColor.opacity(isSelected ? 0 : 0.3), lineWidth: 1)
            )
        }
        .buttonStyle(.plain)
    }
}

/// Color swatch for tag color picker.
struct TagColorSwatchView: View {
    let tagColor: TagColor
    let isSelected: Bool
    var onTap: (() -> Void)?

    var body: some View {
        Button {
            onTap?()
        } label: {
            Circle()
                .fill(tagColor.color)
                .frame(width: 24, height: 24)
                .overlay(
                    Circle()
                        .strokeBorder(Color.primary, lineWidth: isSelected ? 2 : 0)
                        .padding(2)
                )
                .overlay(
                    Image(systemName: "checkmark")
                        .font(.system(size: 10, weight: .bold))
                        .foregroundStyle(.white)
                        .opacity(isSelected ? 1 : 0)
                )
        }
        .buttonStyle(.plain)
        .help(tagColor.displayName)
    }
}

// MARK: - Previews

#Preview("Tag Pills") {
    let sampleTag = Tag(
        id: "1",
        name: "Important",
        color: "#E53935",
        usageCount: 5
    )

    let blueTag = Tag(
        id: "2",
        name: "Work",
        color: "#1E88E5",
        usageCount: 12
    )

    VStack(spacing: 16) {
        Text("Basic Pills")
            .font(.headline)
        HStack {
            TagPillView(tag: sampleTag)
            TagPillView(tag: blueTag)
        }

        Divider()

        Text("With Remove Button")
            .font(.headline)
        HStack {
            TagPillView(tag: sampleTag, showRemoveButton: true) {
                print("Remove tapped")
            }
        }

        Divider()

        Text("Simple Pills")
            .font(.headline)
        HStack {
            SimpleTagPillView(name: "Swift", color: .orange)
            SimpleTagPillView(name: "macOS", color: .purple)
        }

        Divider()

        Text("Selectable Pills")
            .font(.headline)
        HStack {
            SelectableTagPillView(tag: sampleTag, isSelected: true)
            SelectableTagPillView(tag: blueTag, isSelected: false)
        }

        Divider()

        Text("Color Swatches")
            .font(.headline)
        HStack {
            ForEach(Array(TagColor.allCases.prefix(6))) { color in
                TagColorSwatchView(tagColor: color, isSelected: color == .red)
            }
        }
    }
    .padding()
    .frame(width: 300)
}
