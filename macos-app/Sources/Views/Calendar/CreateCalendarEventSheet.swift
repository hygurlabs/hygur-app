import SwiftUI
import EventKit

/// Editable confirmation sheet presented when an LLM tool_call requests the
/// creation of a calendar event. The user reviews the title / time / notes,
/// optionally overrides the calendar, and explicitly confirms or cancels.
///
/// This view is intentionally decoupled from any SSE plumbing — `onConfirm`
/// is invoked with the cleaned-up payload and the sheet does not know how the
/// upstream tool_call surfaced. That keeps it reusable for command-palette
/// quick-add as well, once that lands.
struct CreateCalendarEventSheet: View {
    /// Initial proposal supplied by the model (or by a manual entry-point).
    let initial: CreateCalendarEventProposal
    /// Invoked when the user clicks Save. Receives the (possibly edited) data
    /// AFTER successful EKEventStore.save. The hosting view is in charge of
    /// dismissing the sheet — this view intentionally does NOT call dismiss
    /// itself so the caller controls when the event is recorded.
    let onConfirm: (EKEvent) -> Void
    /// Invoked when the user clicks Cancel or hits Escape.
    let onCancel: () -> Void

    @Environment(CalendarService.self) private var calendar

    @State private var title: String = ""
    @State private var startDate: Date = Date()
    @State private var endDate: Date = Date().addingTimeInterval(3600)
    @State private var notes: String = ""
    @State private var selectedCalendarTitle: String?
    @State private var availableCalendars: [EKCalendar] = []
    @State private var isSaving: Bool = false
    @State private var errorMessage: String?

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            form
            Divider()
            footer
        }
        .frame(minWidth: 480, idealWidth: 520, minHeight: 420, idealHeight: 460)
        .task { await load() }
    }

    // MARK: - Header

    private var header: some View {
        HStack(spacing: HygurSpacing.md) {
            Image(systemName: "calendar.badge.plus")
                .font(.title2)
                .foregroundStyle(HygurColors.accent)
            VStack(alignment: .leading, spacing: 2) {
                Text("Create calendar event")
                    .font(HygurTypography.title3)
                    .fontWeight(.semibold)
                Text("Hygur is asking to add this to your Calendar. Review and confirm.")
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textSecondary)
            }
            Spacer()
        }
        .padding(HygurSpacing.lg)
    }

    // MARK: - Form

    private var form: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: HygurSpacing.md) {
                field(label: "Title") {
                    TextField("Event title", text: $title)
                        .textFieldStyle(.roundedBorder)
                }

                HStack(spacing: HygurSpacing.md) {
                    field(label: "Starts") {
                        DatePicker("", selection: $startDate)
                            .labelsHidden()
                    }
                    field(label: "Ends") {
                        DatePicker("", selection: $endDate, in: startDate...)
                            .labelsHidden()
                    }
                }

                if !availableCalendars.isEmpty {
                    field(label: "Calendar") {
                        Picker("", selection: $selectedCalendarTitle) {
                            ForEach(availableCalendars, id: \.calendarIdentifier) { cal in
                                Text(cal.title).tag(Optional(cal.title))
                            }
                        }
                        .labelsHidden()
                        .pickerStyle(.menu)
                    }
                }

                field(label: "Notes (optional)") {
                    TextEditor(text: $notes)
                        .frame(minHeight: 80, maxHeight: 140)
                        .overlay(
                            RoundedRectangle(cornerRadius: HygurRadius.sm)
                                .strokeBorder(HygurColors.border, lineWidth: 0.5)
                        )
                }

                if let errorMessage {
                    Text(errorMessage)
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.danger)
                }
            }
            .padding(HygurSpacing.lg)
        }
    }

    private func field<Content: View>(
        label: String,
        @ViewBuilder content: () -> Content
    ) -> some View {
        VStack(alignment: .leading, spacing: HygurSpacing.xs) {
            Text(label)
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textTertiary)
                .textCase(.uppercase)
            content()
        }
    }

    // MARK: - Footer

    private var footer: some View {
        HStack {
            Spacer()
            Button("Cancel") {
                onCancel()
            }
            .keyboardShortcut(.escape, modifiers: [])
            Button {
                Task { await save() }
            } label: {
                if isSaving {
                    ProgressView()
                        .controlSize(.small)
                        .tint(.white)
                        .padding(.horizontal, HygurSpacing.sm)
                } else {
                    Text("Add to Calendar")
                }
            }
            .keyboardShortcut(.defaultAction)
            .buttonStyle(.borderedProminent)
            .disabled(isSaving || title.trimmingCharacters(in: .whitespaces).isEmpty)
        }
        .padding(HygurSpacing.lg)
    }

    // MARK: - Lifecycle

    @MainActor
    private func load() async {
        title = initial.title
        startDate = initial.start
        endDate = max(initial.end, initial.start.addingTimeInterval(60 * 15))
        notes = initial.notes ?? ""

        // Trigger the lazy permission flow so the picker and Save are useful.
        _ = await calendar.ensureAuthorized()

        // Pull the writable calendars after authorisation so the picker only
        // surfaces calendars the user can actually save into.
        if calendar.hasFullAccess {
            let raw = EKEventStore.init().calendars(for: .event)
            availableCalendars = raw.filter { $0.allowsContentModifications }
            // Resolve selection: prefer LLM-provided name, else default cal.
            if let name = initial.calendarName,
               let match = availableCalendars.first(where: { $0.title.compare(name, options: .caseInsensitive) == .orderedSame }) {
                selectedCalendarTitle = match.title
            } else if let defaultCal = availableCalendars.first {
                selectedCalendarTitle = defaultCal.title
            }
        }
    }

    @MainActor
    private func save() async {
        errorMessage = nil
        isSaving = true
        defer { isSaving = false }

        let cal = availableCalendars.first { $0.title == selectedCalendarTitle }
        do {
            let event = try await calendar.createEvent(
                title: title.trimmingCharacters(in: .whitespacesAndNewlines),
                start: startDate,
                end: endDate,
                notes: notes.isEmpty ? nil : notes,
                calendar: cal
            )
            onConfirm(event)
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}

/// Plain value type carrying the LLM-supplied initial values into the sheet.
/// Decoupled from `EKEvent` so the sheet works in previews / tests without an
/// active EventKit context.
struct CreateCalendarEventProposal: Hashable, Sendable, Identifiable {
    let id = UUID()
    let title: String
    let start: Date
    let end: Date
    let notes: String?
    let calendarName: String?

    /// Convenience constructor from the JSON the sidecar tool emits.
    /// Returns nil if the timestamps can't be parsed — the chat layer should
    /// log a warning and skip the confirmation sheet rather than show a blank
    /// form when the model hands us garbage.
    static func from(
        title: String,
        startISO: String,
        endISO: String,
        notes: String?,
        calendarName: String?
    ) -> CreateCalendarEventProposal? {
        guard let start = parseISO(startISO), let end = parseISO(endISO) else { return nil }
        return CreateCalendarEventProposal(
            title: title,
            start: start,
            end: end,
            notes: notes,
            calendarName: calendarName
        )
    }

    /// Mirror of the sidecar's `parseISO8601` helper. Tries RFC3339 (with and
    /// without offset) and date-only forms in order. Stays in sync with the Go
    /// side so a roundtrip from LLM → sidecar → swift never loses precision.
    private static func parseISO(_ s: String) -> Date? {
        // RFC3339 with offset, the most common LLM output.
        let rfc = ISO8601DateFormatter()
        rfc.formatOptions = [.withInternetDateTime]
        if let d = rfc.date(from: s) { return d }
        rfc.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let d = rfc.date(from: s) { return d }

        // Local time without offset.
        let local = DateFormatter()
        local.locale = Locale(identifier: "en_US_POSIX")
        local.dateFormat = "yyyy-MM-dd'T'HH:mm:ss"
        if let d = local.date(from: s) { return d }

        // Date-only (treat as start-of-day in user's local TZ).
        let dateOnly = DateFormatter()
        dateOnly.locale = Locale(identifier: "en_US_POSIX")
        dateOnly.dateFormat = "yyyy-MM-dd"
        if let d = dateOnly.date(from: s) { return d }

        return nil
    }
}
