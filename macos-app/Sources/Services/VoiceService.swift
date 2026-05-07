import Foundation
import AVFoundation
import Speech
import AppKit

/// Push-to-talk speech-to-text. Holds the mic open while the caller wants
/// to record, streams partial transcripts as `transcript` (so the UI can
/// echo words live into the input field), and stops cleanly on `stop()`.
///
/// Forces on-device recognition because Hygur is local-first — Apple's
/// server fallback would leak audio off-device.
@MainActor
@Observable
final class VoiceService {
    var isRecording: Bool = false
    var transcript: String = ""
    var error: String?

    /// True once `prepare()` has located a recognizer whose locale has the
    /// on-device language pack installed. The mic button reads this to
    /// disable itself up-front; we deliberately never set `error` from
    /// inside `start()` for "pack missing", because assigning to `error`
    /// mid-tap was triggering the chat's banner overlay and breaking the
    /// window-level layout on macOS 26.
    var isOnDeviceAvailable: Bool = false

    /// The locale picked from `Locale.preferredLanguages`. Surfaced so a
    /// future Settings panel can show which language the mic is using.
    var resolvedLocale: Locale?

    private let audioEngine = AVAudioEngine()
    private var recognizer: SFSpeechRecognizer?
    private var request: SFSpeechAudioBufferRecognitionRequest?
    private var task: SFSpeechRecognitionTask?

    /// Resolves a recognizer that:
    ///   1. matches one of the user's preferred languages (System Settings →
    ///      Language & Region), and
    ///   2. has the on-device language pack installed.
    /// Falling back to `Locale.current` was wrong: a French user with region
    /// United Kingdom hits `en-GB`, which usually has no on-device asset
    /// installed → start() always errored. Walk preferred languages instead.
    func prepare() async {
        if SFSpeechRecognizer.authorizationStatus() == .notDetermined {
            _ = await Self.requestSpeechAuthorization()
        }
        if AVCaptureDevice.authorizationStatus(for: .audio) == .notDetermined {
            _ = await Self.requestMicrophoneAccess()
        }
        guard SFSpeechRecognizer.authorizationStatus() == .authorized else { return }

        let supported = SFSpeechRecognizer.supportedLocales()
        for langID in Locale.preferredLanguages {
            let pref = Locale(identifier: langID)
            let match = supported.first(where: { $0.identifier == pref.identifier })
                ?? supported.first(where: { $0.language.languageCode == pref.language.languageCode })
            guard let match, let r = SFSpeechRecognizer(locale: match) else { continue }
            if r.supportsOnDeviceRecognition {
                recognizer = r
                resolvedLocale = match
                isOnDeviceAvailable = true
                return
            }
        }
    }

    func start() async {
        guard !isRecording else { return }
        // The mic button is disabled when isOnDeviceAvailable is false, so
        // this guard is a defense in depth. We deliberately do NOT set
        // `error` here — see the comment on `isOnDeviceAvailable`.
        guard isOnDeviceAvailable, let recognizer, recognizer.isAvailable else { return }

        error = nil
        transcript = ""

        let micGranted = await Self.requestMicrophoneAccess()
        guard micGranted else {
            error = "Microphone access denied. Enable it in System Settings → Privacy & Security → Microphone."
            return
        }

        let req = SFSpeechAudioBufferRecognitionRequest()
        req.shouldReportPartialResults = true
        req.requiresOnDeviceRecognition = true
        self.request = req

        let inputNode = audioEngine.inputNode
        let format = inputNode.outputFormat(forBus: 0)

        // The tap callback fires on the audio realtime thread. With Swift 6
        // strict concurrency, a closure declared inside a @MainActor method
        // inherits @MainActor isolation, and the runtime traps when the
        // closure is invoked off the main actor. Type the block as
        // @Sendable explicitly, and box the non-Sendable request so the
        // capture is allowed across the isolation boundary.
        let reqBox = UncheckedSendableBox(req)
        let tapBlock: @Sendable (AVAudioPCMBuffer, AVAudioTime) -> Void = { buffer, _ in
            reqBox.value.append(buffer)
        }
        inputNode.installTap(onBus: 0, bufferSize: 1024, format: format, block: tapBlock)

        audioEngine.prepare()
        do {
            try audioEngine.start()
        } catch {
            self.error = "Could not start audio: \(error.localizedDescription)"
            cleanup()
            return
        }

        isRecording = true

        task = recognizer.recognitionTask(with: req) { [weak self] result, taskError in
            guard let self else { return }
            if let result {
                let text = result.bestTranscription.formattedString
                Task { @MainActor [weak self] in
                    self?.transcript = text
                }
            }
            if let taskError {
                let nsError = taskError as NSError
                // 301 = "request was canceled" — fired on every clean stop().
                if nsError.domain == "kAFAssistantErrorDomain" && nsError.code == 301 { return }
                Task { @MainActor [weak self] in
                    self?.error = "Recognition error: \(taskError.localizedDescription)"
                    self?.cleanup()
                }
            }
        }
    }

    func stop() {
        cleanup()
    }

    private func cleanup() {
        if audioEngine.isRunning {
            audioEngine.stop()
            audioEngine.inputNode.removeTap(onBus: 0)
        }
        request?.endAudio()
        request = nil
        task?.cancel()
        task = nil
        isRecording = false
    }

    // MARK: - Permissions

    nonisolated private static func requestSpeechAuthorization() async -> SFSpeechRecognizerAuthorizationStatus {
        await withCheckedContinuation { cont in
            SFSpeechRecognizer.requestAuthorization { status in
                cont.resume(returning: status)
            }
        }
    }

    nonisolated private static func requestMicrophoneAccess() async -> Bool {
        await withCheckedContinuation { cont in
            AVCaptureDevice.requestAccess(for: .audio) { granted in
                cont.resume(returning: granted)
            }
        }
    }
}

/// Crosses an actor-isolation boundary by promising the wrapped value is
/// safe to use across threads — used here to pass a non-Sendable
/// `SFSpeechAudioBufferRecognitionRequest` into the audio realtime tap
/// callback. `append(_:)` on that request is documented as thread-safe.
private struct UncheckedSendableBox<T>: @unchecked Sendable {
    let value: T
    init(_ value: T) { self.value = value }
}
