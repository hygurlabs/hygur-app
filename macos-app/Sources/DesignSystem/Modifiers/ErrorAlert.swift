import SwiftUI

struct ErrorBannerModifier: ViewModifier {
    @Binding var message: String?

    func body(content: Content) -> some View {
        ZStack(alignment: .bottom) {
            content
            if let msg = message {
                HStack(spacing: HygurSpacing.sm) {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(HygurColors.warning)
                    Text(msg)
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textPrimary)
                    Spacer()
                    Button {
                        withAnimation { message = nil }
                    } label: {
                        Image(systemName: "xmark")
                            .foregroundStyle(HygurColors.textSecondary)
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel("Dismiss error")
                }
                .padding(HygurSpacing.md)
                .background(HygurColors.surface)
                .clipShape(RoundedRectangle(cornerRadius: HygurRadius.md))
                .hygurCardShadow()
                .padding(HygurSpacing.lg)
                .transition(.move(edge: .bottom).combined(with: .opacity))
            }
        }
        .animation(.easeInOut(duration: 0.2), value: message)
    }
}

extension View {
    func errorBanner(_ message: Binding<String?>) -> some View {
        modifier(ErrorBannerModifier(message: message))
    }
}
