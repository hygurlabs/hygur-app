import SwiftUI
import AppKit

/// Action buttons that appear on hover over a message bubble
struct MessageActionsView: View {
    let message: Message
    let isLastAssistantMessage: Bool
    let onCopy: () -> Void
    let onRegenerate: (() -> Void)?
    var isSpeaking: Bool = false
    var onSpeak: (() -> Void)? = nil

    @State private var showCopiedFeedback: Bool = false

    var body: some View {
        HStack(spacing: HygurSpacing.xxs) {
            // Copy button
            IconButton(
                systemImage: showCopiedFeedback ? "checkmark" : "doc.on.doc",
                label: "Copy message",
                action: copyToClipboard,
                foregroundColor: showCopiedFeedback ? HygurColors.success : HygurColors.textSecondary
            )

            // Speak button (assistant messages only)
            if message.role == .assistant, let onSpeak {
                IconButton(
                    systemImage: isSpeaking ? "stop.circle" : "speaker.wave.2",
                    label: isSpeaking ? "Stop reading" : "Read aloud",
                    action: onSpeak
                )
            }

            // Regenerate button (only for assistant messages)
            if message.role == .assistant, isLastAssistantMessage, let onRegenerate {
                IconButton(
                    systemImage: "arrow.clockwise",
                    label: "Regenerate response",
                    action: onRegenerate
                )
            }
        }
        .padding(.horizontal, HygurSpacing.sm - 2)
        .padding(.vertical, HygurSpacing.xs)
        .background(
            RoundedRectangle(cornerRadius: HygurRadius.sm)
                .fill(HygurColors.surface)
                .hygurCardShadow()
        )
    }

    private func copyToClipboard() {
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        pasteboard.setString(message.content, forType: .string)

        onCopy()

        // Show checkmark feedback
        withAnimation(.easeInOut(duration: 0.15)) {
            showCopiedFeedback = true
        }

        // Reset after delay
        Task {
            try? await Task.sleep(nanoseconds: 1_500_000_000) // 1.5 seconds
            withAnimation(.easeInOut(duration: 0.15)) {
                showCopiedFeedback = false
            }
        }
    }
}

#Preview {
    VStack(spacing: 20) {
        MessageActionsView(
            message: Message(role: .assistant, content: "Test message"),
            isLastAssistantMessage: true,
            onCopy: {},
            onRegenerate: {}
        )

        MessageActionsView(
            message: Message(role: .user, content: "User message"),
            isLastAssistantMessage: false,
            onCopy: {},
            onRegenerate: nil
        )
    }
    .padding()
}
