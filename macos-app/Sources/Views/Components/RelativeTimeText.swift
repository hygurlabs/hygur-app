import SwiftUI

/// Renders a "time ago" label that auto-refreshes.
///
/// Behaviour:
/// - Under 60 s: live ticking string ("just now", "12 sec ago"), refreshed
///   every second via `TimelineView`.
/// - 60 s and older: dropped to minute/hour/day granularity ("5 min ago",
///   "2 h ago", "3 d ago") — refreshed every 30 s.
///
/// We round seconds out once we cross the minute mark because the second
/// precision is visual noise in places like the Activity log and the menubar
/// panel: by the time a user reads a row, the seconds field is already wrong.
struct RelativeTimeText: View {
    let date: Date

    var body: some View {
        TimelineView(.periodic(from: .now, by: 30)) { context in
            Text(Self.format(date, now: context.date))
        }
    }

    private static let coarseFormatter: RelativeDateTimeFormatter = {
        let f = RelativeDateTimeFormatter()
        f.unitsStyle = .abbreviated
        f.dateTimeStyle = .numeric
        return f
    }()

    private static func format(_ date: Date, now: Date) -> String {
        let elapsed = now.timeIntervalSince(date)
        if elapsed < 5 {
            return "just now"
        }
        if elapsed < 60 {
            return "\(Int(elapsed)) sec ago"
        }
        return coarseFormatter.localizedString(for: date, relativeTo: now)
    }
}
