import Foundation
import AVFoundation
import Speech

/// Push-to-talk speech-to-text. Holds the mic open while the caller wants
/// to record, streams partial transcripts as `transcript` (so the UI can
/// echo words live into the input field), and stops cleanly on `stop()`.
///
/// We force on-device recognition (`requiresOnDeviceRecognition = true`)
/// because Hygur is local-first — Apple's server fallback would leak audio
/// off-device. On systems without the on-device language pack, `start()`
/// surfaces a clear error pointing to the right system setting.
@MainActor
@Observable
final class VoiceService {
    var isRecording: Bool = false
    var transcript: String = ""
    var error: String?

    private let audioEngine = AVAudioEngine()
    private var recognizer: SFSpeechRecognizer?
    private var request: SFSpeechAudioBufferRecognitionRequest?
    private var task: SFSpeechRecognitionTask?

    func start(locale: Locale = .current) async {
        guard !isRecording else { return }
        error = nil
        transcript = ""

        let speechAuth = await Self.requestSpeechAuthorization()
        guard speechAuth == .authorized else {
            error = Self.message(for: speechAuth)
            return
        }

        let micGranted = await Self.requestMicrophoneAccess()
        guard micGranted else {
            error = "Microphone access denied. Enable it in System Settings → Privacy & Security → Microphone."
            return
        }

        let recognizer = SFSpeechRecognizer(locale: locale)
        guard let recognizer, recognizer.isAvailable else {
            error = "Speech recognition is unavailable for \(locale.identifier)."
            return
        }
        guard recognizer.supportsOnDeviceRecognition else {
            error = "On-device speech recognition is not installed for \(locale.identifier). Add the language in System Settings → Keyboard → Dictation."
            return
        }
        self.recognizer = recognizer

        let request = SFSpeechAudioBufferRecognitionRequest()
        request.shouldReportPartialResults = true
        request.requiresOnDeviceRecognition = true
        self.request = request

        let inputNode = audioEngine.inputNode
        let format = inputNode.outputFormat(forBus: 0)
        inputNode.installTap(onBus: 0, bufferSize: 1024, format: format) { [weak self] buffer, _ in
            self?.request?.append(buffer)
        }

        audioEngine.prepare()
        do {
            try audioEngine.start()
        } catch {
            self.error = "Could not start audio: \(error.localizedDescription)"
            cleanup()
            return
        }

        isRecording = true

        task = recognizer.recognitionTask(with: request) { [weak self] result, taskError in
            guard let self else { return }
            if let result {
                let text = result.bestTranscription.formattedString
                Task { @MainActor [weak self] in
                    self?.transcript = text
                }
            }
            if let taskError {
                let nsError = taskError as NSError
                // Code 301 ("Recognition request was canceled") is what we
                // get on a clean stop; suppress it to avoid a false-positive
                // banner. Any other error is real.
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
        recognizer = nil
        isRecording = false
    }

    // MARK: - Permissions

    private static func requestSpeechAuthorization() async -> SFSpeechRecognizerAuthorizationStatus {
        await withCheckedContinuation { cont in
            SFSpeechRecognizer.requestAuthorization { status in
                cont.resume(returning: status)
            }
        }
    }

    private static func requestMicrophoneAccess() async -> Bool {
        await withCheckedContinuation { cont in
            AVCaptureDevice.requestAccess(for: .audio) { granted in
                cont.resume(returning: granted)
            }
        }
    }

    private static func message(for status: SFSpeechRecognizerAuthorizationStatus) -> String {
        switch status {
        case .denied:
            return "Speech recognition denied. Enable it in System Settings → Privacy & Security → Speech Recognition."
        case .restricted:
            return "Speech recognition is restricted on this device."
        case .notDetermined:
            return "Speech recognition permission was not granted."
        case .authorized:
            return ""
        @unknown default:
            return "Speech recognition is unavailable."
        }
    }
}
