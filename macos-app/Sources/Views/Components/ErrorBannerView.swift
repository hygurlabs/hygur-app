import SwiftUI

struct ErrorBannerView: View {
    let message: String
    let onDismiss: () -> Void

    var body: some View {
        HStack(spacing: HygurSpacing.sm) {
            Image(systemName: "exclamationmark.triangle.fill")
                .foregroundStyle(HygurColors.warning)
                .accessibilityHidden(true)

            Text(message)
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textPrimary)
                .fixedSize(horizontal: false, vertical: true)

            Spacer()

            Button {
                onDismiss()
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
        .padding(.horizontal, HygurSpacing.lg)
        .padding(.bottom, HygurSpacing.md)
    }
}

// MARK: - View Extension

extension View {
    /// Overlays a dismissible error banner anchored to the bottom of this view.
    ///
    /// Usage:
    /// ```swift
    /// ContentView()
    ///     .errorBannerOverlay($viewModel.errorMessage)
    /// ```
    func errorBannerOverlay(_ message: Binding<String?>) -> some View {
        ZStack(alignment: .bottom) {
            self
            if let msg = message.wrappedValue {
                ErrorBannerView(message: msg) {
                    withAnimation(.easeInOut(duration: 0.2)) {
                        message.wrappedValue = nil
                    }
                }
                .transition(.move(edge: .bottom).combined(with: .opacity))
            }
        }
        .animation(.easeInOut(duration: 0.2), value: message.wrappedValue)
    }
}

#if DEBUG
struct ErrorBannerView_Previews: PreviewProvider {
    struct PreviewWrapper: View {
        @State private var errorMessage: String? = "Failed to sync knowledge base. Check your connection and try again."

        var body: some View {
            ZStack {
                Color(.windowBackgroundColor)
                    .ignoresSafeArea()
                VStack {
                    Text("Content area")
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
            .errorBannerOverlay($errorMessage)
            .onAppear { errorMessage = "Failed to sync knowledge base. Check your connection and try again." }
        }
    }

    static var previews: some View {
        PreviewWrapper()
            .frame(width: 500, height: 300)
    }
}
#endif
