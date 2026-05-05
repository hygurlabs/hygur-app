import SwiftUI

enum LoadingStyle {
    case small      // ProgressView controlSize .small — for toolbars
    case large      // ProgressView with .scaleEffect(1.5) — for empty content areas
    case streaming  // pulsing dot — replaces ProcessingDot
    case thinking   // bouncing dots — replaces ThinkingDotsView
}

struct LoadingIndicator: View {
    let style: LoadingStyle

    var body: some View {
        switch style {
        case .small:
            ProgressView()
                .controlSize(.small)

        case .large:
            ProgressView()
                .scaleEffect(1.5)

        case .streaming:
            StreamingDot()

        case .thinking:
            ThinkingDots()
        }
    }
}

// MARK: - Streaming Dot

/// Pulsing glow dot used during AI streaming responses. Replaces `ProcessingDot` in ChatView.
private struct StreamingDot: View {
    @State private var glowing = false

    var body: some View {
        Circle()
            .fill(HygurColors.accent)
            .frame(width: 8, height: 8)
            .scaleEffect(glowing ? 1.2 : 1.0)
            .opacity(glowing ? 1.0 : 0.6)
            .animation(
                .easeInOut(duration: 0.6).repeatForever(autoreverses: true),
                value: glowing
            )
            .onAppear { glowing = true }
    }
}

// MARK: - Thinking Dots

/// Three bouncing dots used during AI thinking state. Replaces `ThinkingDotsView` in ChatView.
private struct ThinkingDots: View {
    @State private var animationPhase = 0
    private let timer = Timer.publish(every: 0.2, on: .main, in: .common).autoconnect()

    var body: some View {
        HStack(spacing: HygurSpacing.xs) {
            ForEach(0..<3, id: \.self) { index in
                Circle()
                    .fill(HygurColors.textSecondary)
                    .frame(width: 6, height: 6)
                    .scaleEffect(animationPhase == index ? 1.3 : 1.0)
                    .animation(.easeInOut(duration: 0.2), value: animationPhase)
            }
        }
        .onReceive(timer) { _ in
            animationPhase = (animationPhase + 1) % 3
        }
    }
}

#if DEBUG
#Preview {
    VStack(spacing: 24) {
        HStack(spacing: 20) {
            VStack(spacing: 8) {
                LoadingIndicator(style: .small)
                Text("small").font(.caption)
            }
            VStack(spacing: 8) {
                LoadingIndicator(style: .large)
                Text("large").font(.caption)
            }
            VStack(spacing: 8) {
                LoadingIndicator(style: .streaming)
                Text("streaming").font(.caption)
            }
            VStack(spacing: 8) {
                LoadingIndicator(style: .thinking)
                Text("thinking").font(.caption)
            }
        }
    }
    .padding(32)
}
#endif
