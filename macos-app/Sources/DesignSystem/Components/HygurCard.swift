import SwiftUI

/// Unified card primitive — entity rows (notes, mail, knowledge, citation
/// sources) compose this so the app reads consistently. Header is always
/// rendered; body and footer slots are opt-in via `@ViewBuilder` defaults.
struct HygurCard<Content: View, Footer: View, Accessory: View>: View {
    let icon: String
    let iconTint: Color
    let title: String
    let subtitle: String?
    let isSelected: Bool
    let fillContainer: Bool
    let accessory: Accessory
    let content: Content
    let footer: Footer

    @State private var isHovered = false

    init(
        icon: String,
        iconTint: Color = HygurColors.accent,
        title: String,
        subtitle: String? = nil,
        isSelected: Bool = false,
        fillContainer: Bool = false,
        @ViewBuilder accessory: () -> Accessory = { EmptyView() },
        @ViewBuilder content: () -> Content = { EmptyView() },
        @ViewBuilder footer: () -> Footer = { EmptyView() }
    ) {
        self.icon = icon
        self.iconTint = iconTint
        self.title = title
        self.subtitle = subtitle
        self.isSelected = isSelected
        self.fillContainer = fillContainer
        self.accessory = accessory()
        self.content = content()
        self.footer = footer()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            HStack(alignment: .firstTextBaseline, spacing: HygurSpacing.sm) {
                Image(systemName: icon)
                    .font(.system(size: 13, weight: .medium))
                    .foregroundStyle(iconTint)
                    .frame(width: 16)
                VStack(alignment: .leading, spacing: 2) {
                    Text(title)
                        .font(HygurTypography.cardTitle)
                        .foregroundStyle(HygurColors.textPrimary)
                        .lineLimit(1)
                    if let subtitle {
                        Text(subtitle)
                            .font(HygurTypography.cardMeta)
                            .foregroundStyle(HygurColors.textSecondary)
                            .lineLimit(1)
                    }
                }
                Spacer(minLength: 0)
                accessory
            }

            content

            if fillContainer {
                Spacer(minLength: 0)
            }

            footer
        }
        .padding(HygurSpacing.md)
        .frame(
            maxWidth: fillContainer ? .infinity : nil,
            maxHeight: fillContainer ? .infinity : nil,
            alignment: .topLeading
        )
        .background(
            RoundedRectangle(cornerRadius: HygurRadius.md)
                .fill(HygurColors.surface)
        )
        .overlay(
            RoundedRectangle(cornerRadius: HygurRadius.md)
                .strokeBorder(
                    isSelected ? HygurColors.brandBlue : HygurColors.border,
                    lineWidth: isSelected ? 1.5 : 0.5
                )
        )
        .hygurCardShadow()
        .scaleEffect(isHovered ? 1.005 : 1.0)
        .animation(.easeOut(duration: 0.12), value: isHovered)
        .onHover { isHovered = $0 }
    }
}
