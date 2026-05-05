import SwiftUI
import AppKit

// MARK: - Knowledge Graph View

struct KnowledgeGraphView: View {
    @State private var viewModel = KnowledgeGraphViewModel()
    @State private var selectedNodeId: String?

    var body: some View {
        VStack(spacing: 0) {
            GraphFilterBar(viewModel: viewModel)
            Divider()
            ZStack(alignment: .trailing) {
                graphCanvas

                if let id = selectedNodeId, let node = viewModel.graphNode(id: id) {
                    NodeDetailPanel(node: node, onOpen: openNode)
                        .frame(width: 280)
                        .frame(maxHeight: .infinity)
                        .background(HygurColors.background)
                        .hygurOverlayShadow()
                        .transition(.move(edge: .trailing).combined(with: .opacity))
                }
            }
        }
        .task {
            await viewModel.loadGraph()
        }
        .alert("Error", isPresented: $viewModel.showError) {
            Button("OK") { viewModel.showError = false }
        } message: {
            Text(viewModel.errorMessage)
        }
    }

    // MARK: - Graph Canvas

    private var graphCanvas: some View {
        ZStack {
            Color(nsColor: .controlBackgroundColor)

            if viewModel.isLoading {
                VStack(spacing: HygurSpacing.sm) {
                    LoadingIndicator(style: .large)
                    Text("Loading graph...")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                }
            } else if viewModel.renderNodes.isEmpty {
                emptyState
            } else {
                GeometryReader { geo in
                    ForceDirectedGraphCanvas(
                        viewModel: viewModel,
                        selectedNodeId: $selectedNodeId,
                        size: geo.size
                    )
                }
            }
        }
        .overlay(alignment: .topTrailing) { legendView.padding() }
        .overlay(alignment: .bottomTrailing) { controlsView.padding() }
    }

    // MARK: - Empty State

    private var emptyState: some View {
        VStack(spacing: 16) {
            Image(systemName: "circle.hexagongrid")
                .font(.system(size: 48))
                .foregroundStyle(.secondary)
                .accessibilityHidden(true)
            Text("No data to visualize")
                .font(.headline)
                .foregroundStyle(.secondary)
            Text("Add some notes, documents, or tags to see the knowledge graph")
                .font(.subheadline)
                .foregroundStyle(.tertiary)
                .multilineTextAlignment(.center)
        }
        .padding()
    }

    // MARK: - Legend

    private var legendView: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.xs) {
            Text("Legend")
                .font(HygurTypography.caption)
                .fontWeight(.semibold)
                .foregroundStyle(HygurColors.textPrimary)
            legendRow(Color(nsColor: .systemPurple), "Tag")
            legendRow(Color(nsColor: .systemOrange), "Project")
            legendRow(HygurColors.sourceTypeColor("note"), "Note")
            legendRow(HygurColors.sourceTypeColor("html"), "Email")
            legendRow(HygurColors.sourceTypeColor("markdown"), "Document")
        }
        .padding(HygurSpacing.sm)
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: HygurRadius.md))
    }

    private func legendRow(_ color: Color, _ label: String) -> some View {
        HStack(spacing: HygurSpacing.xs) {
            Circle().fill(color).frame(width: 10, height: 10)
            Text(label).font(HygurTypography.caption)
        }
    }

    // MARK: - Controls

    private var controlsView: some View {
        HStack(spacing: HygurSpacing.sm) {
            IconButton(systemImage: "arrow.counterclockwise", label: "Reset layout") {
                Task { await viewModel.resetSimulation() }
            }
            IconButton(
                systemImage: viewModel.isSimulating ? "pause.fill" : "play.fill",
                label: viewModel.isSimulating ? "Pause simulation" : "Resume simulation"
            ) {
                viewModel.toggleSimulation()
            }
        }
        .padding(HygurSpacing.xs)
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: HygurRadius.md))
    }

    // MARK: - Actions

    private func openNode(_ node: GraphNode) {
        guard let path = node.sourcePath else { return }
        if node.sourceType == "mail" || node.sourceType == "email" {
            if let url = URL(string: "message://\(path)") {
                NSWorkspace.shared.open(url)
            }
        } else {
            NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: path)])
        }
    }
}

// MARK: - Filter Bar

/// Top-of-canvas filter strip. Lets the user scope what's rendered without
/// hitting the backend again — the view model holds the full dataset and
/// re-derives the visible subset whenever a chip toggles.
private struct GraphFilterBar: View {
    @Bindable var viewModel: KnowledgeGraphViewModel

    private let timeOptions: [(label: String, days: Int?)] = [
        ("All time", nil),
        ("90 d",     90),
        ("30 d",     30),
        ("7 d",      7),
    ]

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 10) {
                typeToggle("Tags",     count: viewModel.typeCounts.tags,     isOn: $viewModel.filter.showTags,     color: .purple)
                typeToggle("Projects", count: viewModel.typeCounts.projects, isOn: $viewModel.filter.showProjects, color: .orange)
                typeToggle("Items",    count: viewModel.typeCounts.items,    isOn: $viewModel.filter.showItems,    color: .blue)

                Spacer()

                Picker("Window", selection: timePickerBinding) {
                    ForEach(timeOptions, id: \.label) { opt in
                        Text(opt.label).tag(opt.days)
                    }
                }
                .pickerStyle(.menu)
                .frame(maxWidth: 110)

                searchField

                Text("\(viewModel.visibleNodeCount) / \(viewModel.totalNodeCount)")
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
                    .help("Visible nodes vs. total")
            }
            .padding(.horizontal, 12)
            .padding(.top, 8)
            .padding(.bottom, 4)

            // Items source-type sub-filter — only useful when items are visible.
            if viewModel.filter.showItems {
                HStack(spacing: 6) {
                    sourceChip("Email",    key: "email")
                    sourceChip("Note",     key: "note")
                    sourceChip("Document", key: "markdown", aliases: ["md", "file"])
                    sourceChip("PDF",      key: "pdf")
                    Spacer()
                }
                .padding(.horizontal, 12)
                .padding(.bottom, 6)
            }
        }
        .background(HygurColors.background)
    }

    private func typeToggle(_ label: String, count: Int, isOn: Binding<Bool>, color: Color) -> some View {
        Button {
            isOn.wrappedValue.toggle()
        } label: {
            HStack(spacing: 6) {
                Circle().fill(color).frame(width: 8, height: 8)
                Text(label).font(.callout)
                Text("(\(count))").font(.caption).foregroundStyle(.secondary)
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 4)
            .background(
                RoundedRectangle(cornerRadius: 6)
                    .fill(isOn.wrappedValue ? color.opacity(0.15) : Color.clear)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 6)
                    .strokeBorder(isOn.wrappedValue ? color.opacity(0.4) : Color.secondary.opacity(0.25), lineWidth: 1)
            )
            .opacity(isOn.wrappedValue ? 1.0 : 0.55)
        }
        .buttonStyle(.plain)
    }

    /// Source-type chip. Some logical groups span multiple keys (e.g.
    /// "Document" covers markdown / md / file) so an aliases list is allowed.
    private func sourceChip(_ label: String, key: String, aliases: [String] = []) -> some View {
        let allKeys = Set([key] + aliases)
        let isOn = !viewModel.filter.sourceTypes.isDisjoint(with: allKeys)
        return Button {
            var s = viewModel.filter.sourceTypes
            if isOn { s.subtract(allKeys) } else { s.formUnion(allKeys) }
            viewModel.filter.sourceTypes = s
        } label: {
            Text(label)
                .font(.caption)
                .padding(.horizontal, 8)
                .padding(.vertical, 3)
                .background(
                    RoundedRectangle(cornerRadius: 5)
                        .fill(isOn ? Color.accentColor.opacity(0.15) : Color.clear)
                )
                .overlay(
                    RoundedRectangle(cornerRadius: 5)
                        .strokeBorder(isOn ? Color.accentColor.opacity(0.4) : Color.secondary.opacity(0.2), lineWidth: 1)
                )
                .opacity(isOn ? 1.0 : 0.55)
        }
        .buttonStyle(.plain)
    }

    private var searchField: some View {
        HStack(spacing: 4) {
            Image(systemName: "magnifyingglass").font(.caption).foregroundStyle(.secondary)
            TextField("Search labels", text: $viewModel.filter.search)
                .textFieldStyle(.plain)
                .font(.callout)
                .frame(maxWidth: 160)
            if !viewModel.filter.search.isEmpty {
                Button { viewModel.filter.search = "" } label: {
                    Image(systemName: "xmark.circle.fill").font(.caption).foregroundStyle(.secondary)
                }
                .buttonStyle(.plain)
            }
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 4)
        .background(.quaternary.opacity(0.6), in: RoundedRectangle(cornerRadius: 6))
    }

    private var timePickerBinding: Binding<Int?> {
        Binding(
            get: { viewModel.filter.sinceDays },
            set: { viewModel.filter.sinceDays = $0 }
        )
    }
}

// MARK: - Force Directed Canvas

struct ForceDirectedGraphCanvas: View {
    let viewModel: KnowledgeGraphViewModel
    @Binding var selectedNodeId: String?
    let size: CGSize

    @State private var scale: CGFloat = 1.0
    @State private var offset: CGSize = .zero
    @State private var draggedNodeId: String?
    @State private var dragStartPosition: CGPoint = .zero
    @State private var dragStartOffset: CGSize = .zero

    var body: some View {
        // Accessing these here registers @Observable tracking, so Canvas redraws when they change
        let positions = viewModel.positions
        let edges = viewModel.edges
        let nodes = viewModel.renderNodes

        Canvas { context, canvasSize in
            let tx = CGAffineTransform(
                translationX: offset.width + canvasSize.width / 2,
                y: offset.height + canvasSize.height / 2
            ).scaledBy(x: scale, y: scale)

            // Edges
            for edge in edges {
                guard let sp = positions[edge.source], let tp = positions[edge.target] else { continue }
                var path = Path()
                path.move(to: sp.applying(tx))
                path.addLine(to: tp.applying(tx))
                context.stroke(path, with: .color(.gray.opacity(0.3)), lineWidth: 1)
            }

            // Nodes + labels
            // Label strategy: avoid visual clutter
            //   scale < 0.6  → no labels
            //   0.6–1.0      → tags and projects only (structural nodes)
            //   > 1.0        → all nodes
            // Selected node always gets its label.
            // Labels are truncated to keep them readable.
            let labelsForAll   = scale > 1.0
            let labelsForMajor = scale > 0.6
            for node in nodes {
                guard let pos = positions[node.id] else { continue }
                let screenPos = pos.applying(tx)
                let r = node.nodeSize * scale
                let rect = CGRect(x: screenPos.x - r / 2, y: screenPos.y - r / 2, width: r, height: r)

                context.fill(Circle().path(in: rect), with: .color(node.displayColor))

                let isSelected = selectedNodeId == node.id
                if isSelected {
                    context.stroke(
                        Circle().path(in: rect.insetBy(dx: -3, dy: -3)),
                        with: .color(.white), lineWidth: 2
                    )
                }

                let showLabel = isSelected
                    || labelsForAll
                    || (labelsForMajor && (node.type == .tag || node.type == .project))
                if showLabel {
                    let raw = node.label
                    let truncated = raw.count > 22 ? String(raw.prefix(20)) + "…" : raw
                    let fontSize: CGFloat = isSelected ? max(10, 12 * scale) : max(9, 10 * scale)
                    context.draw(
                        Text(truncated)
                            .font(.system(size: fontSize, weight: isSelected ? .semibold : .regular))
                            .foregroundColor(isSelected ? .primary : .secondary),
                        at: CGPoint(x: screenPos.x, y: screenPos.y + r / 2 + 3),
                        anchor: .top
                    )
                }
            }
        }
        .gesture(
            DragGesture(minimumDistance: 2)
                .onChanged { value in
                    if draggedNodeId == nil {
                        if let id = hitTest(value.startLocation) {
                            draggedNodeId = id
                            dragStartPosition = viewModel.positions[id] ?? .zero
                            Task { await viewModel.fixNode(id) }
                        } else {
                            dragStartOffset = offset
                        }
                    }
                    if let id = draggedNodeId {
                        let newPos = CGPoint(
                            x: dragStartPosition.x + value.translation.width / scale,
                            y: dragStartPosition.y + value.translation.height / scale
                        )
                        viewModel.positions[id] = newPos
                        Task { await viewModel.moveFixedNode(id, to: newPos) }
                    } else {
                        offset = CGSize(
                            width: dragStartOffset.width + value.translation.width,
                            height: dragStartOffset.height + value.translation.height
                        )
                    }
                }
                .onEnded { _ in
                    if let id = draggedNodeId {
                        Task { await viewModel.releaseNode(id) }
                    }
                    draggedNodeId = nil
                }
        )
        .gesture(
            MagnificationGesture()
                .onChanged { value in
                    scale = max(0.2, min(4.0, value))
                }
        )
        .onTapGesture { location in
            withAnimation(.easeInOut(duration: 0.15)) {
                selectedNodeId = hitTest(location) != selectedNodeId ? hitTest(location) : nil
            }
        }
        .onAppear {
            Task {
                await viewModel.initializePositions(in: size)
                viewModel.startSimulation()
            }
        }
        .onDisappear {
            viewModel.stopSimulation()
        }
        .onChange(of: viewModel.viewportResetToken) { _, _ in
            withAnimation(.easeInOut(duration: 0.35)) {
                scale = 1.0
                offset = .zero
            }
        }
    }

    private func hitTest(_ location: CGPoint) -> String? {
        let tx = CGAffineTransform(
            translationX: offset.width + size.width / 2,
            y: offset.height + size.height / 2
        ).scaledBy(x: scale, y: scale)

        for node in viewModel.renderNodes {
            guard let pos = viewModel.positions[node.id] else { continue }
            let screenPos = pos.applying(tx)
            let r = node.nodeSize * scale / 2 + 5
            if hypot(location.x - screenPos.x, location.y - screenPos.y) < r {
                return node.id
            }
        }
        return nil
    }
}

// MARK: - Node Detail Panel

struct NodeDetailPanel: View {
    let node: GraphNode
    let onOpen: (GraphNode) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.lg) {
            header
            Divider()
            metadata
            Spacer()
            actions
        }
        .padding(HygurSpacing.lg)
        .background(HygurColors.background)
    }

    private var header: some View {
        HStack(spacing: HygurSpacing.md) {
            Image(systemName: node.icon)
                .font(.title)
                .foregroundColor(node.displayColor)

            VStack(alignment: .leading, spacing: HygurSpacing.xxs) {
                Text(node.label)
                    .font(HygurTypography.headline)
                    .lineLimit(2)
                Text(node.type.rawValue.capitalized)
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textSecondary)
            }
        }
    }

    private var metadata: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            if let sourceType = node.sourceType { metadataRow("Type", sourceType) }
            if let path = node.sourcePath { metadataRow("Path", path) }
            if let created = node.createdAt { metadataRow("Created", formatDate(created)) }
            if let updated = node.updatedAt { metadataRow("Updated", formatDate(updated)) }
        }
    }

    private func metadataRow(_ label: String, _ value: String) -> some View {
        VStack(alignment: .leading, spacing: HygurSpacing.xxs) {
            Text(label)
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textSecondary)
            Text(value)
                .font(HygurTypography.callout)
                .lineLimit(2)
        }
    }

    private var actions: some View {
        Group {
            if node.sourcePath != nil {
                Button {
                    onOpen(node)
                } label: {
                    Label(openButtonLabel, systemImage: openButtonIcon)
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
            }
        }
    }

    private var openButtonLabel: String {
        node.sourceType == "mail" || node.sourceType == "email" ? "Open in Mail" : "Show in Finder"
    }

    private var openButtonIcon: String {
        node.sourceType == "mail" || node.sourceType == "email" ? "envelope" : "folder"
    }

    private func formatDate(_ isoString: String) -> String {
        let formatter = ISO8601DateFormatter()
        let display = DateFormatter()
        display.dateStyle = .medium
        display.timeStyle = .short

        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let date = formatter.date(from: isoString) { return display.string(from: date) }
        formatter.formatOptions = [.withInternetDateTime]
        if let date = formatter.date(from: isoString) { return display.string(from: date) }
        return isoString
    }
}

// MARK: - View Model

/// `GraphFilter` scopes what's rendered. The view model keeps the full
/// dataset returned by the backend (`allNodes` / `allEdges`) and re-derives
/// `renderNodes` / `edges` whenever the filter changes — the simulator only
/// ever sees the filtered subset, so layout stays tractable on dense graphs.
struct GraphFilter: Equatable {
    /// Which top-level node types to include.
    var showTags: Bool = true
    var showProjects: Bool = true
    var showItems: Bool = true
    /// When showItems is on, narrow further by source_type.
    var sourceTypes: Set<String> = ["email", "note", "file", "markdown", "md", "pdf", "html"]
    /// Items with createdAt older than this fall out. Nil = no time bound.
    var sinceDays: Int? = nil
    /// Substring filter applied to node labels (case-insensitive).
    var search: String = ""
    /// Hide tags whose only connections are to filtered-out items.
    var hideOrphans: Bool = true

    static let `default` = GraphFilter(showItems: false) // start with structure only
}

@MainActor
@Observable
final class KnowledgeGraphViewModel {
    /// The raw response from /graph. Never mutated post-load.
    private(set) var allNodes: [GraphNode] = []
    private(set) var allEdges: [GraphEdge] = []

    /// Filtered subset shown in the canvas.
    private(set) var renderNodes: [GraphNode] = []
    var positions: [String: CGPoint] = [:]
    private(set) var edges: [GraphEdge] = []

    /// User-controlled filter. Mutating this re-derives the visible subset
    /// and reseeds the simulator.
    var filter: GraphFilter = .default {
        didSet {
            guard filter != oldValue else { return }
            Task { await applyFilter() }
        }
    }

    var isLoading = false
    var isSimulating = false
    var showError = false
    var errorMessage = ""
    /// Incremented on reset — canvas observes this to snap scale/offset back to default
    var viewportResetToken: Int = 0

    private let sidecar = SidecarService.fromSettings()
    private let simulator = GraphSimulator()
    private var simulationTask: Task<Void, Never>?
    private var lastCanvasSize: CGSize = CGSize(width: 800, height: 600)

    func graphNode(id: String) -> GraphNode? {
        renderNodes.first { $0.id == id }
    }

    func updateCanvasSize(_ size: CGSize) {
        lastCanvasSize = size
    }

    /// Stats for the filter toolbar (e.g. "showing 102 of 755 nodes").
    var totalNodeCount: Int { allNodes.count }
    var visibleNodeCount: Int { renderNodes.count }

    /// Counts per node type — used to label the filter chips.
    var typeCounts: (tags: Int, projects: Int, items: Int) {
        var t = 0, p = 0, i = 0
        for n in allNodes {
            switch n.type {
            case .tag: t += 1
            case .project: p += 1
            case .item: i += 1
            }
        }
        return (t, p, i)
    }

    func loadGraph() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let response = try await sidecar.getGraph()
            allNodes = response.nodes
            allEdges = response.edges
            await applyFilter()
        } catch {
            errorMessage = error.localizedDescription
            showError = true
        }
    }

    /// Re-derive renderNodes/edges from allNodes/allEdges using the active
    /// filter, then reseed the simulator. Cheap to call — runs in O(N+E).
    func applyFilter() async {
        stopSimulation()
        let (filteredNodes, filteredEdges) = computeFilteredSubset()
        renderNodes = filteredNodes
        edges = filteredEdges
        await simulator.setup(nodes: filteredNodes, edges: filteredEdges)
        await simulator.initializePositions(in: lastCanvasSize)
        positions = await simulator.snapshots()
        viewportResetToken += 1
        startSimulation()
    }

    private func computeFilteredSubset() -> ([GraphNode], [GraphEdge]) {
        let needle = filter.search.trimmingCharacters(in: .whitespaces).lowercased()
        let cutoff: Date? = filter.sinceDays.map { Date().addingTimeInterval(-Double($0) * 86400) }
        let dateFormatter = ISO8601DateFormatter()
        dateFormatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let dateFormatterFallback = ISO8601DateFormatter()
        dateFormatterFallback.formatOptions = [.withInternetDateTime]

        // First pass: type + source_type + search + time
        var keptIds = Set<String>()
        var kept: [GraphNode] = []
        for n in allNodes {
            // Type gate
            switch n.type {
            case .tag where !filter.showTags: continue
            case .project where !filter.showProjects: continue
            case .item where !filter.showItems: continue
            case .item:
                if let st = n.sourceType, !filter.sourceTypes.contains(st) { continue }
            default: break
            }
            // Search gate (label substring)
            if !needle.isEmpty && !n.label.lowercased().contains(needle) { continue }
            // Time gate (only items have createdAt)
            if let cutoff, n.type == .item, let raw = n.createdAt {
                let date = dateFormatter.date(from: raw) ?? dateFormatterFallback.date(from: raw)
                if let date, date < cutoff { continue }
            }
            keptIds.insert(n.id)
            kept.append(n)
        }

        // Edges: keep only those connecting two kept nodes.
        let keptEdges = allEdges.filter { keptIds.contains($0.source) && keptIds.contains($0.target) }

        // Optionally drop tags/projects whose only role was to connect now-filtered items.
        if filter.hideOrphans {
            let connected = Set(keptEdges.flatMap { [$0.source, $0.target] })
            kept = kept.filter { node in
                // Items with no surviving edge become noise on a structural map; keep them
                // when items are explicitly enabled, drop them otherwise.
                if node.type == .item { return filter.showItems }
                return connected.contains(node.id)
            }
            keptIds = Set(kept.map { $0.id })
        }
        return (kept, keptEdges.filter { keptIds.contains($0.source) && keptIds.contains($0.target) })
    }

    func initializePositions(in size: CGSize) async {
        lastCanvasSize = size
        // Skip if positions are already populated for the current node set —
        // applyFilter() already seeded them and the canvas's onAppear would
        // otherwise destroy that work and force another expensive reseed.
        if positions.isEmpty || positions.count != renderNodes.count {
            await simulator.initializePositions(in: size)
            positions = await simulator.snapshots()
        }
    }

    func startSimulation() {
        guard !isSimulating else { return }
        isSimulating = true
        let sim = simulator
        simulationTask = Task.detached(priority: .userInitiated) { [weak self] in
            // Pre-warm: run 150 ticks silently in one actor hop (no sleep, no UI update).
            // This gets nodes to a rough stable layout before showing any animation,
            // eliminating the initial flickering phase.
            let (preConverged, initialSnaps) = await sim.batchTick(count: 150)
            await MainActor.run {
                guard let self else { return }
                self.positions = initialSnaps
                if preConverged { self.isSimulating = false }
            }
            guard !preConverged && !Task.isCancelled else { return }

            // Animate remaining convergence at ~60fps
            while !Task.isCancelled {
                let (done, snaps) = await sim.tickAndSnapshot()
                await MainActor.run {
                    guard let self else { return }
                    self.positions = snaps
                    if done { self.isSimulating = false }
                }
                if done { break }
                try? await Task.sleep(nanoseconds: 16_000_000)
            }
        }
    }

    func stopSimulation() {
        simulationTask?.cancel()
        simulationTask = nil
        isSimulating = false
    }

    func toggleSimulation() {
        if isSimulating { stopSimulation() } else { startSimulation() }
    }

    func resetSimulation() async {
        stopSimulation()
        viewportResetToken += 1  // tells the canvas to reset scale + offset
        await simulator.initializePositions(in: lastCanvasSize)
        positions = await simulator.snapshots()
        startSimulation()
    }

    func fixNode(_ id: String) async {
        await simulator.setFixed(id: id, value: true)
    }

    func moveFixedNode(_ id: String, to position: CGPoint) async {
        await simulator.moveNode(id: id, to: position)
    }

    func releaseNode(_ id: String) async {
        await simulator.setFixed(id: id, value: false)
        if !isSimulating { startSimulation() }
    }
}

// MARK: - Simulation Actor (off main thread)

actor GraphSimulator {
    private struct SimNode {
        var position: CGPoint
        var velocity: CGVector = .zero
        var isFixed: Bool = false
    }

    private var nodeIds: [String] = []
    private var nodes: [SimNode] = []
    private var neighborLists: [[Int]] = []
    private var indexMap: [String: Int] = [:]

    // Force-directed parameters tuned for graphs of up to a few thousand nodes.
    // The previous tuning (threshold 0.4, damping 0.80, maxIter 800) caused
    // perpetual sub-pixel jitter on dense email graphs: velocities oscillated
    // around 0.5-1.0 forever, the convergence check never fired, and the Canvas
    // kept re-rendering. This tuning damps faster, accepts micro-jitter as
    // converged, and snaps tiny velocities to zero so they can't feed the next
    // tick's force calculation.
    private let repulsionStrength: Double = 3000
    private let attractionStrength: Double = 0.03
    private let centeringStrength: Double = 0.005
    private let damping: Double = 0.85
    private let minDistance: Double = 40
    private let velocityThreshold: Double = 1.5  // accept micro-jitter as converged
    private let velocitySnapToZero: Double = 0.5 // kill tiny velocities each tick
    private let maxIterations: Int = 250         // ~4 s at 60 fps — plenty for a good layout
    private let convergenceFrames: Int = 8       // require N stable frames before stopping

    private var iterationCount: Int = 0
    private var stableFrameCount: Int = 0

    func setup(nodes: [GraphNode], edges: [GraphEdge]) {
        nodeIds = nodes.map { $0.id }
        indexMap = Dictionary(uniqueKeysWithValues: nodeIds.enumerated().map { ($1, $0) })
        self.nodes = nodes.map { _ in SimNode(position: .zero) }

        // Pre-build adjacency by index — done once, not per frame
        neighborLists = Array(repeating: [], count: nodes.count)
        for edge in edges {
            if let si = indexMap[edge.source], let ti = indexMap[edge.target] {
                neighborLists[si].append(ti)
                neighborLists[ti].append(si)
            }
        }
        iterationCount = 0
        stableFrameCount = 0
    }

    func initializePositions(in size: CGSize) {
        let count = nodes.count
        guard count > 0 else { return }
        let radius = min(size.width, size.height) * 0.55
        for i in 0..<count {
            let angle = 2 * Double.pi * Double(i) / Double(count)
            // Independent x/y jitter ensures no two nodes share an exact position
            let jx = Double.random(in: -25...25)
            let jy = Double.random(in: -25...25)
            nodes[i].position = CGPoint(x: cos(angle) * radius + jx, y: sin(angle) * radius + jy)
            nodes[i].velocity = .zero
        }
        iterationCount = 0
        stableFrameCount = 0
    }

    /// Runs one physics step and returns (converged, positions).
    /// Combines tick + snapshot to halve actor-hop count in the simulation loop.
    func tickAndSnapshot() -> (Bool, [String: CGPoint]) {
        let converged = tick()
        return (converged, buildSnapshot())
    }

    /// Runs up to `count` ticks in one actor hop without sleeping — used for pre-warming
    /// the layout silently before showing any animation to the user.
    func batchTick(count: Int) -> (Bool, [String: CGPoint]) {
        var converged = false
        for _ in 0..<count {
            converged = tick()
            if converged { break }
        }
        return (converged, buildSnapshot())
    }

    func snapshots() -> [String: CGPoint] {
        buildSnapshot()
    }

    func setFixed(id: String, value: Bool) {
        guard let i = indexMap[id] else { return }
        nodes[i].isFixed = value
        if !value { nodes[i].velocity = .zero }
    }

    func moveNode(id: String, to position: CGPoint) {
        guard let i = indexMap[id] else { return }
        nodes[i].position = position
        nodes[i].velocity = .zero
    }

    // MARK: - Physics Step (Barnes-Hut O(N log N))

    private func tick() -> Bool {
        let count = nodes.count
        guard count > 1 else { return true }

        iterationCount += 1
        if iterationCount > maxIterations { return true }

        // Compute bounding box for quadtree
        var minX = nodes[0].position.x, maxX = minX
        var minY = nodes[0].position.y, maxY = minY
        for node in nodes {
            if node.position.x < minX { minX = node.position.x }
            if node.position.x > maxX { maxX = node.position.x }
            if node.position.y < minY { minY = node.position.y }
            if node.position.y > maxY { maxY = node.position.y }
        }
        let pad: Double = 50
        let tree = QuadTree(bounds: CGRect(x: minX - pad, y: minY - pad,
                                          width: (maxX - minX) + 2 * pad,
                                          height: (maxY - minY) + 2 * pad))
        for i in 0..<count {
            tree.insert(index: i, position: nodes[i].position)
        }

        // Apply forces
        for i in 0..<count {
            guard !nodes[i].isFixed else { continue }

            // Repulsion via Barnes-Hut quadtree (O(N log N))
            var force = tree.repulsionForce(from: nodes[i].position, excluding: i,
                                            theta: 0.8, strength: repulsionStrength,
                                            minDist: minDistance)

            // Attraction along edges (already O(degree))
            for ni in neighborLists[i] {
                let dx = nodes[ni].position.x - nodes[i].position.x
                let dy = nodes[ni].position.y - nodes[i].position.y
                force.dx += dx * attractionStrength
                force.dy += dy * attractionStrength
            }

            // Weak centering
            force.dx -= nodes[i].position.x * centeringStrength
            force.dy -= nodes[i].position.y * centeringStrength

            // Euler integration with damping + velocity clamp (prevents NaN from blowing up)
            var vx = (nodes[i].velocity.dx + force.dx) * damping
            var vy = (nodes[i].velocity.dy + force.dy) * damping
            if vx > 50 { vx = 50 } else if vx < -50 { vx = -50 }
            if vy > 50 { vy = 50 } else if vy < -50 { vy = -50 }
            // Snap to zero below the snap threshold so micro-perturbations
            // don't accumulate frame-to-frame and keep the graph blinking.
            if abs(vx) < velocitySnapToZero { vx = 0 }
            if abs(vy) < velocitySnapToZero { vy = 0 }
            nodes[i].velocity.dx = vx
            nodes[i].velocity.dy = vy
            nodes[i].position.x += vx
            nodes[i].position.y += vy
        }

        // Convergence requires N consecutive stable frames. A single quiet
        // frame from a still-jittery graph used to fire false positives;
        // demanding 8 frames in a row eliminates that without delaying real
        // convergence noticeably (~130 ms at 60 fps).
        if isQuietFrame {
            stableFrameCount += 1
        } else {
            stableFrameCount = 0
        }
        return stableFrameCount >= convergenceFrames
    }

    private var isQuietFrame: Bool {
        nodes.allSatisfy {
            $0.isFixed ||
            (abs($0.velocity.dx) < velocityThreshold && abs($0.velocity.dy) < velocityThreshold)
        }
    }

    private func buildSnapshot() -> [String: CGPoint] {
        var result = [String: CGPoint](minimumCapacity: nodeIds.count)
        for i in 0..<nodeIds.count {
            result[nodeIds[i]] = nodes[i].position
        }
        return result
    }
}

// MARK: - Barnes-Hut Quadtree

private final class QuadTree {
    private let bounds: CGRect
    private var com: CGPoint = .zero     // center of mass
    private var totalMass: Double = 0
    private var bodyIndex: Int = -1      // ≥ 0 only for leaf nodes
    private var leafPos: CGPoint = .zero // position of the leaf body
    private var nw, ne, sw, se: QuadTree?

    init(bounds: CGRect) {
        self.bounds = bounds
    }

    func insert(index: Int, position: CGPoint) {
        if totalMass == 0 {
            // Empty leaf
            bodyIndex = index
            leafPos = position
            com = position
            totalMass = 1
            return
        }

        // Update center of mass to include new body
        let newMass = totalMass + 1
        com = CGPoint(x: (com.x * totalMass + position.x) / newMass,
                      y: (com.y * totalMass + position.y) / newMass)
        totalMass = newMass

        // Guard against infinite recursion: bodies at identical or near-identical positions
        // would always fall into the same child quadrant, subdividing forever.
        // Below 1pt we stop subdividing and keep multiple bodies in a single "bucket" leaf.
        guard bounds.width > 1.0 else { return }

        if bodyIndex >= 0 {
            // Non-empty leaf → promote to internal node
            let existingIndex = bodyIndex
            let existingPos = leafPos
            bodyIndex = -1
            child(for: existingPos).insert(index: existingIndex, position: existingPos)
        }

        child(for: position).insert(index: index, position: position)
    }

    /// Compute repulsion force on a point from all bodies in this subtree.
    /// Iterative implementation (explicit stack) — eliminates any risk of call-stack overflow
    /// regardless of tree depth or node clustering.
    func repulsionForce(from p: CGPoint, excluding ex: Int,
                        theta: Double, strength: Double, minDist: Double) -> CGVector {
        var force = CGVector.zero
        var stack: [QuadTree] = []
        stack.reserveCapacity(32)
        stack.append(self)

        while let node = stack.popLast() {
            guard node.totalMass > 0 else { continue }

            // Leaf or bucket
            if node.bodyIndex >= 0 {
                if node.bodyIndex == ex {
                    guard node.totalMass > 1 else { continue }
                    let f = QuadTree.repForce(from: p, to: node.com,
                                             mass: node.totalMass - 1, strength: strength, minDist: minDist)
                    force.dx += f.dx; force.dy += f.dy
                } else {
                    let f = QuadTree.repForce(from: p, to: node.com,
                                             mass: node.totalMass, strength: strength, minDist: minDist)
                    force.dx += f.dx; force.dy += f.dy
                }
                continue
            }

            // Internal node: apply Barnes-Hut criterion
            let dx = p.x - node.com.x
            let dy = p.y - node.com.y
            let dist = hypot(dx, dy)

            if dist > 0 && node.bounds.width / dist < theta {
                // Cell far enough — use center-of-mass approximation
                let f = QuadTree.repForce(from: p, to: node.com,
                                         mass: node.totalMass, strength: strength, minDist: minDist)
                force.dx += f.dx; force.dy += f.dy
            } else {
                // Too close — push children onto stack
                if let c = node.nw { stack.append(c) }
                if let c = node.ne { stack.append(c) }
                if let c = node.sw { stack.append(c) }
                if let c = node.se { stack.append(c) }
            }
        }

        return force
    }

    // MARK: - Private

    private static func repForce(from p: CGPoint, to body: CGPoint, mass: Double, strength: Double, minDist: Double) -> CGVector {
        let dx = p.x - body.x
        let dy = p.y - body.y
        let dist = max(hypot(dx, dy), minDist)
        let magnitude = strength * mass / (dist * dist)
        return CGVector(dx: dx / dist * magnitude, dy: dy / dist * magnitude)
    }

    private func child(for position: CGPoint) -> QuadTree {
        let mx = bounds.midX, my = bounds.midY
        let hw = bounds.width / 2, hh = bounds.height / 2

        if position.x >= mx {
            if position.y >= my {
                if ne == nil { ne = QuadTree(bounds: CGRect(x: mx, y: my, width: hw, height: hh)) }
                return ne!
            } else {
                if se == nil { se = QuadTree(bounds: CGRect(x: mx, y: bounds.minY, width: hw, height: hh)) }
                return se!
            }
        } else {
            if position.y >= my {
                if nw == nil { nw = QuadTree(bounds: CGRect(x: bounds.minX, y: my, width: hw, height: hh)) }
                return nw!
            } else {
                if sw == nil { sw = QuadTree(bounds: CGRect(x: bounds.minX, y: bounds.minY, width: hw, height: hh)) }
                return sw!
            }
        }
    }
}

// MARK: - Preview

#Preview("Knowledge Graph") {
    KnowledgeGraphView()
        .frame(width: 800, height: 600)
}
