import Foundation
import AVFoundation

/// Streaming text-to-speech for assistant replies.
///
/// `feed(_:)` accepts deltas as the model emits tokens; whenever a full
/// sentence is buffered (terminated by `.`, `!`, or `?`), an utterance is
/// queued so audio starts before the answer is complete. `finish()` flushes
/// any non-terminated tail at end-of-stream. `speak(_:)` is the one-shot
/// path used by the per-bubble speaker button on completed messages.
///
/// AVSpeechSynthesizer queues utterances internally — calling `speak` while
/// already speaking simply chains the next sentence after the current one,
/// which is exactly the behavior we want for streaming.
@MainActor
@Observable
final class SpeechService: NSObject {
    var isSpeaking: Bool = false

    private let synthesizer = AVSpeechSynthesizer()
    /// Pending characters that haven't yet formed a complete sentence.
    private var buffer: String = ""
    /// User-selected voice locale (defaults to system locale).
    private var locale: Locale = .current

    override init() {
        super.init()
        synthesizer.delegate = self
    }

    /// One-shot: speak the entire string. Replaces anything already queued.
    func speak(_ text: String, locale: Locale = .current) {
        stop()
        self.locale = locale
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        enqueue(trimmed)
    }

    /// Streaming mode: append `delta` and speak any newly-completed
    /// sentences. Call `finish()` at end-of-stream to flush the tail.
    func feed(_ delta: String, locale: Locale = .current) {
        self.locale = locale
        buffer.append(delta)
        while let endIndex = nextSentenceTerminator(in: buffer) {
            let sentence = String(buffer[..<buffer.index(after: endIndex)])
                .trimmingCharacters(in: .whitespacesAndNewlines)
            buffer.removeSubrange(buffer.startIndex...endIndex)
            if !sentence.isEmpty {
                enqueue(sentence)
            }
        }
    }

    /// Speak whatever is left in the buffer, even without a terminator.
    func finish() {
        let tail = buffer.trimmingCharacters(in: .whitespacesAndNewlines)
        buffer = ""
        if !tail.isEmpty {
            enqueue(tail)
        }
    }

    /// Cancel any speech in progress and drop the buffer.
    func stop() {
        if synthesizer.isSpeaking || synthesizer.isPaused {
            synthesizer.stopSpeaking(at: .immediate)
        }
        buffer = ""
        isSpeaking = false
    }

    // MARK: - Internals

    private func enqueue(_ text: String) {
        let utterance = AVSpeechUtterance(string: text)
        utterance.voice = bestVoice(for: locale) ?? AVSpeechSynthesisVoice(language: locale.identifier)
        synthesizer.speak(utterance)
        isSpeaking = true
    }

    /// Prefer a higher-quality voice if installed (Premium/Enhanced), fall
    /// back to the default Compact voice otherwise.
    private func bestVoice(for locale: Locale) -> AVSpeechSynthesisVoice? {
        let lang = locale.identifier.replacingOccurrences(of: "_", with: "-")
        let voices = AVSpeechSynthesisVoice.speechVoices().filter { voice in
            voice.language == lang || voice.language.hasPrefix(String(lang.prefix(2)))
        }
        if let premium = voices.first(where: { $0.quality == .premium }) { return premium }
        if let enhanced = voices.first(where: { $0.quality == .enhanced }) { return enhanced }
        return voices.first
    }

    /// Find the index of the next sentence-ending punctuation followed by
    /// whitespace or end-of-string. Avoids splitting on numbers like
    /// "1.5" or abbreviations like "e.g." by requiring the next char to be
    /// whitespace or absent.
    private func nextSentenceTerminator(in text: String) -> String.Index? {
        let terminators: Set<Character> = [".", "!", "?", "。", "！", "？"]
        var idx = text.startIndex
        while idx < text.endIndex {
            if terminators.contains(text[idx]) {
                let next = text.index(after: idx)
                if next == text.endIndex { return nil } // wait for more
                if text[next].isWhitespace || text[next].isNewline {
                    return idx
                }
            }
            idx = text.index(after: idx)
        }
        return nil
    }
}

extension SpeechService: AVSpeechSynthesizerDelegate {
    nonisolated func speechSynthesizer(_ synthesizer: AVSpeechSynthesizer, didFinish utterance: AVSpeechUtterance) {
        Task { @MainActor [weak self] in
            guard let self else { return }
            // Re-read via our own retained reference to avoid sending a
            // non-Sendable parameter across actors. AVSpeechSynthesizer's
            // isSpeaking is documented as thread-safe.
            if !self.synthesizer.isSpeaking {
                self.isSpeaking = false
            }
        }
    }

    nonisolated func speechSynthesizer(_ synthesizer: AVSpeechSynthesizer, didCancel utterance: AVSpeechUtterance) {
        Task { @MainActor [weak self] in
            self?.isSpeaking = false
        }
    }
}
