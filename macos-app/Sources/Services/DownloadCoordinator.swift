import Foundation

/// Bridges `URLSessionDownloadDelegate` callbacks into async/await for the
/// updater. Owns one download at a time — instantiate fresh for each transfer.
///
/// The delegate-based download API is the only way to observe progress from
/// `URLSession`; `download(for:)` skips straight to completion. We capture
/// progress through `onProgress` and resume the awaiting caller via a checked
/// continuation when the file lands on disk (or fails).
final class DownloadCoordinator: NSObject, URLSessionDownloadDelegate, @unchecked Sendable {
    private let destinationDirectory: URL
    private let expectedSize: Int64
    private let onProgress: @Sendable (Double) -> Void

    private let lock = NSLock()
    private var continuation: CheckedContinuation<URL, Error>?

    init(
        destinationDirectory: URL,
        expectedSize: Int64,
        onProgress: @escaping @Sendable (Double) -> Void
    ) {
        self.destinationDirectory = destinationDirectory
        self.expectedSize = expectedSize
        self.onProgress = onProgress
    }

    /// Start the task and suspend until the download completes (success or error).
    func start(task: URLSessionDownloadTask) async throws -> URL {
        try await withCheckedThrowingContinuation { (cont: CheckedContinuation<URL, Error>) in
            lock.lock()
            continuation = cont
            lock.unlock()
            task.resume()
        }
    }

    private func resumeOnce(_ result: Result<URL, Error>) {
        lock.lock()
        let cont = continuation
        continuation = nil
        lock.unlock()
        switch result {
        case .success(let url): cont?.resume(returning: url)
        case .failure(let err): cont?.resume(throwing: err)
        }
    }

    // MARK: - URLSessionDownloadDelegate

    func urlSession(
        _ session: URLSession,
        downloadTask: URLSessionDownloadTask,
        didWriteData bytesWritten: Int64,
        totalBytesWritten: Int64,
        totalBytesExpectedToWrite: Int64
    ) {
        // GitHub's CDN doesn't always populate Content-Length; fall back to the
        // size we know from the release asset metadata when that happens.
        let total = totalBytesExpectedToWrite > 0 ? totalBytesExpectedToWrite : expectedSize
        guard total > 0 else { return }
        let progress = min(1.0, max(0.0, Double(totalBytesWritten) / Double(total)))
        onProgress(progress)
    }

    func urlSession(
        _ session: URLSession,
        downloadTask: URLSessionDownloadTask,
        didFinishDownloadingTo location: URL
    ) {
        // The temp file vanishes as soon as this method returns — move it
        // synchronously to a stable location we own.
        let dest = destinationDirectory
            .appendingPathComponent("hygur-update-\(UUID().uuidString).dmg")
        do {
            try FileManager.default.moveItem(at: location, to: dest)
            resumeOnce(.success(dest))
        } catch {
            resumeOnce(.failure(error))
        }
    }

    func urlSession(
        _ session: URLSession,
        task: URLSessionTask,
        didCompleteWithError error: Error?
    ) {
        // `didFinishDownloadingTo` fires before this on success, so the
        // continuation is already nil-ed out and we no-op. On HTTP errors
        // (404/403) we get here without a finish callback.
        if let error {
            resumeOnce(.failure(error))
        }
    }
}
