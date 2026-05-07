import AppKit
import UniformTypeIdentifiers

/// Principal class for the `HygurShare` macOS share extension.
///
/// macOS share extensions still use AppKit (no SwiftUI scene API on
/// extensions in macOS 26), so this is a programmatic `NSViewController`
/// that builds a small confirmation panel:
///   - Title (filled from the page title / first line of text / image name)
///   - One-line preview of the captured content
///   - Optional comma-separated tag list (free-form text field)
///   - Save / Cancel buttons
///
/// On Save we POST to the sidecar (URL/text → `/notes`, image → file
/// ingest). On Cancel — or any failure — we call `cancelRequest` with a
/// descriptive error so the host app's share sheet displays a banner.
@MainActor
final class ShareViewController: NSViewController {

    // MARK: - Captured payload

    /// Filled in `loadView` / `viewDidLoad` from `extensionContext.inputItems`.
    /// Both URL+text can co-exist (Safari often passes the page URL plus a
    /// title/excerpt); we prefer the URL since text without a URL has lower
    /// fidelity for re-discovery in the KB.
    private var capturedURL: URL?
    private var capturedText: String?
    private var capturedImagePath: String?
    private var previewTitle: String = ""

    // MARK: - UI

    private let titleLabel = NSTextField(labelWithString: "Save to Hygur")
    private let previewLabel = NSTextField(wrappingLabelWithString: "")
    private let tagsField = NSTextField(string: "")
    private let statusLabel = NSTextField(labelWithString: "")
    private let saveButton = NSButton(title: "Save", target: nil, action: nil)
    private let cancelButton = NSButton(title: "Cancel", target: nil, action: nil)

    // MARK: - Lifecycle

    override func loadView() {
        // Manual NSViewController + Auto Layout setup. The system applies
        // its own chrome around our view, so we only need a panel-sized
        // content area.
        let root = NSView(frame: NSRect(x: 0, y: 0, width: 380, height: 220))
        self.view = root
        configureSubviews(in: root)
    }

    override func viewDidLoad() {
        super.viewDidLoad()
        ingestInputItems()
    }

    // MARK: - Layout

    private func configureSubviews(in root: NSView) {
        titleLabel.font = .systemFont(ofSize: 14, weight: .semibold)
        titleLabel.translatesAutoresizingMaskIntoConstraints = false

        previewLabel.maximumNumberOfLines = 3
        previewLabel.lineBreakMode = .byTruncatingTail
        previewLabel.font = .systemFont(ofSize: 12)
        previewLabel.textColor = .secondaryLabelColor
        previewLabel.translatesAutoresizingMaskIntoConstraints = false

        let tagsLabel = NSTextField(labelWithString: "Tags (comma-separated)")
        tagsLabel.font = .systemFont(ofSize: 11)
        tagsLabel.textColor = .secondaryLabelColor
        tagsLabel.translatesAutoresizingMaskIntoConstraints = false

        tagsField.placeholderString = "research, inbox"
        tagsField.translatesAutoresizingMaskIntoConstraints = false

        statusLabel.font = .systemFont(ofSize: 11)
        statusLabel.textColor = .systemRed
        statusLabel.maximumNumberOfLines = 2
        statusLabel.translatesAutoresizingMaskIntoConstraints = false

        cancelButton.target = self
        cancelButton.action = #selector(cancelTapped)
        cancelButton.bezelStyle = .rounded
        cancelButton.keyEquivalent = "\u{1b}" // Escape
        cancelButton.translatesAutoresizingMaskIntoConstraints = false

        saveButton.target = self
        saveButton.action = #selector(saveTapped)
        saveButton.bezelStyle = .rounded
        saveButton.keyEquivalent = "\r" // Default button
        saveButton.translatesAutoresizingMaskIntoConstraints = false

        root.addSubview(titleLabel)
        root.addSubview(previewLabel)
        root.addSubview(tagsLabel)
        root.addSubview(tagsField)
        root.addSubview(statusLabel)
        root.addSubview(cancelButton)
        root.addSubview(saveButton)

        NSLayoutConstraint.activate([
            titleLabel.topAnchor.constraint(equalTo: root.topAnchor, constant: 16),
            titleLabel.leadingAnchor.constraint(equalTo: root.leadingAnchor, constant: 16),
            titleLabel.trailingAnchor.constraint(equalTo: root.trailingAnchor, constant: -16),

            previewLabel.topAnchor.constraint(equalTo: titleLabel.bottomAnchor, constant: 6),
            previewLabel.leadingAnchor.constraint(equalTo: root.leadingAnchor, constant: 16),
            previewLabel.trailingAnchor.constraint(equalTo: root.trailingAnchor, constant: -16),

            tagsLabel.topAnchor.constraint(equalTo: previewLabel.bottomAnchor, constant: 14),
            tagsLabel.leadingAnchor.constraint(equalTo: root.leadingAnchor, constant: 16),

            tagsField.topAnchor.constraint(equalTo: tagsLabel.bottomAnchor, constant: 4),
            tagsField.leadingAnchor.constraint(equalTo: root.leadingAnchor, constant: 16),
            tagsField.trailingAnchor.constraint(equalTo: root.trailingAnchor, constant: -16),

            statusLabel.topAnchor.constraint(equalTo: tagsField.bottomAnchor, constant: 10),
            statusLabel.leadingAnchor.constraint(equalTo: root.leadingAnchor, constant: 16),
            statusLabel.trailingAnchor.constraint(equalTo: root.trailingAnchor, constant: -16),

            cancelButton.bottomAnchor.constraint(equalTo: root.bottomAnchor, constant: -12),
            cancelButton.trailingAnchor.constraint(equalTo: saveButton.leadingAnchor, constant: -8),
            saveButton.bottomAnchor.constraint(equalTo: root.bottomAnchor, constant: -12),
            saveButton.trailingAnchor.constraint(equalTo: root.trailingAnchor, constant: -16),
            saveButton.widthAnchor.constraint(greaterThanOrEqualToConstant: 80),
        ])
    }

    // MARK: - Input parsing

    /// Walks `extensionContext.inputItems` until we find the first URL,
    /// text, or image attachment we recognize. Share extensions can receive
    /// multiple items but our activation rules cap counts at 1, so the
    /// first hit is always sufficient.
    private func ingestInputItems() {
        guard let items = extensionContext?.inputItems as? [NSExtensionItem] else { return }

        let group = DispatchGroup()
        var foundURL: URL?
        var foundText: String?
        var foundImagePath: String?
        var foundTitle: String?

        for item in items {
            if foundTitle == nil, let title = item.attributedTitle?.string ?? item.attributedContentText?.string {
                foundTitle = title
            }
            for provider in item.attachments ?? [] {
                if provider.hasItemConformingToTypeIdentifier(UTType.url.identifier) {
                    group.enter()
                    provider.loadItem(forTypeIdentifier: UTType.url.identifier, options: nil) { value, _ in
                        if let url = value as? URL { foundURL = url }
                        else if let s = value as? String, let url = URL(string: s) { foundURL = url }
                        group.leave()
                    }
                } else if provider.hasItemConformingToTypeIdentifier(UTType.plainText.identifier) {
                    group.enter()
                    provider.loadItem(forTypeIdentifier: UTType.plainText.identifier, options: nil) { value, _ in
                        if let s = value as? String { foundText = s }
                        group.leave()
                    }
                } else if provider.hasItemConformingToTypeIdentifier(UTType.image.identifier) {
                    group.enter()
                    provider.loadFileRepresentation(forTypeIdentifier: UTType.image.identifier) { url, _ in
                        if let url {
                            // Copy to App Group container so the file
                            // survives after this callback returns and is
                            // readable by the sidecar.
                            let dest = ShareViewController.copyToSharedContainer(url)
                            foundImagePath = dest?.path
                        }
                        group.leave()
                    }
                }
            }
        }

        // Update UI on completion. Wait synchronously up to a short budget;
        // the providers above all resolve from local pasteboard data so
        // this is effectively instant.
        group.notify(queue: .main) { [weak self] in
            guard let self else { return }
            self.capturedURL = foundURL
            self.capturedText = foundText
            self.capturedImagePath = foundImagePath
            self.previewTitle = foundTitle ?? ""
            self.refreshPreview()
        }
    }

    private static func copyToSharedContainer(_ source: URL) -> URL? {
        let suite = "group.com.hygur.shared"
        guard let container = FileManager.default
            .containerURL(forSecurityApplicationGroupIdentifier: suite) else {
            return nil
        }
        let folder = container.appendingPathComponent("Inbox", isDirectory: true)
        try? FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        let dest = folder.appendingPathComponent("\(UUID().uuidString)-\(source.lastPathComponent)")
        do {
            try FileManager.default.copyItem(at: source, to: dest)
            return dest
        } catch {
            return nil
        }
    }

    private func refreshPreview() {
        if let url = capturedURL {
            titleLabel.stringValue = previewTitle.isEmpty
                ? "Save link to Hygur"
                : "Save '\(previewTitle.prefix(60))'"
            previewLabel.stringValue = url.absoluteString
        } else if let text = capturedText {
            titleLabel.stringValue = "Save selection to Hygur"
            previewLabel.stringValue = String(text.prefix(280))
        } else if let path = capturedImagePath {
            titleLabel.stringValue = "Save image to Hygur"
            previewLabel.stringValue = (path as NSString).lastPathComponent
        } else {
            titleLabel.stringValue = "Save to Hygur"
            previewLabel.stringValue = "Nothing recognized in the share payload."
            saveButton.isEnabled = false
        }
    }

    // MARK: - Actions

    @objc private func cancelTapped() {
        let err = NSError(
            domain: "com.hygur.share",
            code: NSUserCancelledError,
            userInfo: [NSLocalizedDescriptionKey: "Cancelled by user"]
        )
        extensionContext?.cancelRequest(withError: err)
    }

    @objc private func saveTapped() {
        saveButton.isEnabled = false
        cancelButton.isEnabled = false
        statusLabel.textColor = .secondaryLabelColor
        statusLabel.stringValue = "Saving..."

        let tags = parseTags(tagsField.stringValue)

        Task {
            do {
                if let url = capturedURL {
                    try await ShareSidecarClient.ingestURL(url, title: previewTitle, tags: tags)
                } else if let text = capturedText {
                    try await ShareSidecarClient.ingestText(text, tags: tags)
                } else if let path = capturedImagePath {
                    try await ShareSidecarClient.ingestFile(at: path, tags: tags)
                } else {
                    throw ShareSidecarClient.IngestError.invalidPayload
                }
                self.extensionContext?.completeRequest(returningItems: nil)
            } catch let error as ShareSidecarClient.IngestError {
                self.statusLabel.textColor = .systemRed
                self.statusLabel.stringValue = error.userFacingMessage
                self.saveButton.isEnabled = true
                self.cancelButton.isEnabled = true
            } catch {
                self.statusLabel.textColor = .systemRed
                self.statusLabel.stringValue = error.localizedDescription
                self.saveButton.isEnabled = true
                self.cancelButton.isEnabled = true
            }
        }
    }

    private func parseTags(_ raw: String) -> [String] {
        raw.split(separator: ",")
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
    }
}
