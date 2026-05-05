import SwiftUI

struct AgendaSheet: View {
    let actions: [AgendaAction]

    var body: some View {
        NavigationStack {
            Group {
                if actions.isEmpty {
                    ContentUnavailableView(
                        "Aucune action imminente",
                        systemImage: "checkmark.circle",
                        description: Text("Pas d'échéances dans les 48 prochaines heures.")
                    )
                } else {
                    List(actions) { action in
                        AgendaActionRow(action: action)
                    }
                    .listStyle(.inset)
                }
            }
            .navigationTitle("Actions urgentes")
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Fermer") {
                        // Dismiss handled by parent
                    }
                }
            }
        }
        .frame(minWidth: 400, minHeight: 300)
    }
}

// MARK: - Action Row

private struct AgendaActionRow: View {
    let action: AgendaAction

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                PriorityPill(priority: action.priority)
                Text(action.what)
                    .font(.body)
                    .fontWeight(.medium)
                    .lineLimit(2)
                Spacer()
            }
            HStack(spacing: 12) {
                Label(relativeDeadline(from: action.deadlineISO), systemImage: "calendar")
                    .font(.caption)
                    .foregroundStyle(.secondary)

                Spacer()

                Button("Voir le doc") {
                    NotificationCenter.default.post(
                        name: .navigateToSection,
                        object: "knowledgeBase"
                    )
                }
                .font(.caption)
                .buttonStyle(.borderless)
                .foregroundStyle(.blue)
            }
        }
        .padding(.vertical, 4)
    }

    private func relativeDeadline(from isoDate: String) -> String {
        let formatter = agendaDateFormatter()
        guard let date = formatter.date(from: isoDate) else { return isoDate }
        return relativeString(for: date)
    }

    private func relativeString(for date: Date) -> String {
        let now = Date()
        let components = Calendar.current.dateComponents([.hour, .day], from: now, to: date)
        let days = components.day ?? 0
        let hours = components.hour ?? 0

        if hours <= 0 && days <= 0 {
            return "Passé"
        } else if days == 0 {
            return "dans \(hours)h"
        } else if days == 1 {
            return "demain"
        } else {
            return "dans \(days) jours"
        }
    }
}

// MARK: - Priority Pill

private struct PriorityPill: View {
    let priority: String

    private var backgroundColor: Color {
        switch priority {
        case "high":   return .red.opacity(0.15)
        case "medium": return .orange.opacity(0.15)
        default:       return Color(.systemGray).opacity(0.15)
        }
    }

    private var foregroundColor: Color {
        switch priority {
        case "high":   return .red
        case "medium": return .orange
        default:       return .secondary
        }
    }

    private var label: String {
        switch priority {
        case "high":   return "Urgent"
        case "medium": return "Moyen"
        default:       return "Bas"
        }
    }

    var body: some View {
        Text(label)
            .font(.caption2)
            .fontWeight(.semibold)
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(backgroundColor, in: Capsule())
            .foregroundStyle(foregroundColor)
    }
}

// MARK: - Date Formatter Factory (Swift 6: no static let on non-Sendable types)

private func agendaDateFormatter() -> DateFormatter {
    let f = DateFormatter()
    f.dateFormat = "yyyy-MM-dd"
    f.locale = Locale(identifier: "en_US_POSIX")
    f.timeZone = TimeZone(identifier: "UTC")
    return f
}

#Preview {
    AgendaSheet(actions: [
        AgendaAction(what: "Envoyer rapport Q2", deadlineISO: "2026-05-06", priority: "high", sourceId: "doc-1", confidence: 1.0),
        AgendaAction(what: "Réviser contrat", deadlineISO: "2026-05-07", priority: "medium", sourceId: "doc-2", confidence: 0.9),
    ])
}
