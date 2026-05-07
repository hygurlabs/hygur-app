import SwiftUI
import AppKit

/// Native `NSSearchField` wrapped for SwiftUI. Gives the canonical macOS
/// rounded search appearance (magnifying glass + clear button) — used at the
/// leading edge of feature-view toolbars where `.searchable` can't be placed
/// (it always docks trailing on macOS).
struct ToolbarSearchField: NSViewRepresentable {
    @Binding var text: String
    var prompt: String = "Search"
    var width: CGFloat = 240

    func makeNSView(context: Context) -> NSSearchField {
        let field = NSSearchField()
        field.placeholderString = prompt
        field.delegate = context.coordinator
        field.sendsSearchStringImmediately = true
        field.sendsWholeSearchString = false
        field.controlSize = .regular
        field.bezelStyle = .roundedBezel
        field.target = context.coordinator
        field.action = #selector(Coordinator.searchChanged(_:))
        field.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            field.widthAnchor.constraint(equalToConstant: width)
        ])
        return field
    }

    func updateNSView(_ nsView: NSSearchField, context: Context) {
        if nsView.stringValue != text {
            nsView.stringValue = text
        }
        nsView.placeholderString = prompt
        context.coordinator.parent = self
    }

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    final class Coordinator: NSObject, NSSearchFieldDelegate {
        var parent: ToolbarSearchField

        init(_ parent: ToolbarSearchField) { self.parent = parent }

        @objc func searchChanged(_ sender: NSSearchField) {
            parent.text = sender.stringValue
        }
    }
}
