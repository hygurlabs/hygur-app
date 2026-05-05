import SwiftUI

enum BadgeStyle {
    case capsule   // fully rounded
    case rounded   // corner radius sm
    case tag       // like capsule but with click interaction support
}

struct BadgeView: View {
    let text: String
    var color: Color = .secondary
    var style: BadgeStyle = .capsule
    var icon: String? = nil
    var size: Font = HygurTypography.caption

    var body: some View {
        HStack(spacing: HygurSpacing.xs) {
            if let icon = icon {
                Image(systemName: icon)
                    .font(.system(size: 9))
            }
            Text(text)
                .font(size)
                .fontWeight(.medium)
        }
        .padding(.horizontal, HygurSpacing.sm - 2)  // 6
        .padding(.vertical, HygurSpacing.xs - 1)    // 3
        .background(color.opacity(0.15))
        .foregroundStyle(color)
        .clipShape(badgeShape)
    }

    private var badgeShape: AnyShape {
        switch style {
        case .capsule, .tag:
            AnyShape(Capsule())
        case .rounded:
            AnyShape(RoundedRectangle(cornerRadius: HygurRadius.sm))
        }
    }
}

#if DEBUG
#Preview {
    VStack(spacing: 12) {
        HStack(spacing: 8) {
            BadgeView(text: "Capsule", color: .blue)
            BadgeView(text: "Rounded", color: .green, style: .rounded)
            BadgeView(text: "Tag", color: .orange, style: .tag)
        }
        HStack(spacing: 8) {
            BadgeView(text: "With Icon", color: .purple, icon: "tag.fill")
            BadgeView(text: "Danger", color: HygurColors.danger)
            BadgeView(text: "Success", color: HygurColors.success)
        }
    }
    .padding()
}
#endif
