import SwiftUI

/// Layout choice for entity lists (notes, mail, knowledge base, …).
/// Persisted per-view via `@AppStorage` keys like `hygur.layout.notes`.
enum ViewLayoutMode: String, CaseIterable, Identifiable {
    case list
    case grid

    var id: String { rawValue }

    var systemImage: String {
        switch self {
        case .list: return "list.bullet"
        case .grid: return "square.grid.2x2"
        }
    }

    var label: String {
        switch self {
        case .list: return "List"
        case .grid: return "Grid"
        }
    }
}

/// Compact segmented toggle that flips between list and grid layouts.
/// Drop into a view's header next to refresh / new buttons.
struct ViewLayoutToggle: View {
    @Binding var mode: ViewLayoutMode

    var body: some View {
        Picker("Layout", selection: $mode) {
            ForEach(ViewLayoutMode.allCases) { option in
                Image(systemName: option.systemImage)
                    .help(option.label)
                    .tag(option)
            }
        }
        .pickerStyle(.segmented)
        .labelsHidden()
        .fixedSize()
        .help("Toggle list / grid layout")
    }
}
