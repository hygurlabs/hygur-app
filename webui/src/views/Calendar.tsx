import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { Bell, CalendarClock, Sparkles } from "lucide-react";
import { api } from "../lib/api";
import { native } from "../lib/native";
import { useDetail } from "../components/DetailPanel";
import { RecordList } from "../components/RecordList";
import { fmtDate, fmtDateTime } from "../lib/format";
import {
  Button,
  EmptyState,
  ErrorBanner,
  Page,
  PageHeader,
  Skeleton,
  ToggleGroup,
} from "../components/ui";

const ENABLED_KEY = "hygur.calendar.enabled";
const SELECTED_KEY = "hygur.calendar.selected";

function SectionLabel({ children }: { children: string }) {
  return (
    <h2 className="mb-2 mt-9 text-[11.5px] font-medium uppercase tracking-[0.09em] text-faint first:mt-0">
      {children}
    </h2>
  );
}

export function Calendar() {
  const isNative = native.available;
  const openDetail = useDetail();
  const navigate = useNavigate();
  const [enabled, setEnabled] = useState(
    () => localStorage.getItem(ENABLED_KEY) === "1",
  );
  const [selected, setSelected] = useState<string[]>(() => {
    try {
      return JSON.parse(localStorage.getItem(SELECTED_KEY) ?? "[]");
    } catch {
      return [];
    }
  });

  function toggle(id: string) {
    setSelected((prev) => {
      const next = prev.includes(id)
        ? prev.filter((x) => x !== id)
        : [...prev, id];
      localStorage.setItem(SELECTED_KEY, JSON.stringify(next));
      return next;
    });
  }

  async function connect() {
    const ok = await native.calendar.authorize();
    if (ok) {
      localStorage.setItem(ENABLED_KEY, "1");
      setEnabled(true);
    }
  }

  // Mirror the briefing selection to the native side so the meeting-briefing
  // scheduler knows which calendars to watch (empty = all). Gated by `enabled`.
  useEffect(() => {
    if (!isNative) return;
    void native.calendar.setEnabled(enabled, selected);
  }, [isNative, enabled, selected]);

  const calendars = useQuery({
    queryKey: ["native-calendars"],
    queryFn: () => native.calendar.listCalendars(),
    enabled: isNative && enabled,
  });

  const events = useQuery({
    queryKey: ["native-events"],
    queryFn: () => native.calendar.listEvents(24 * 7),
    enabled: isNative && enabled,
  });

  const agenda = useQuery({
    queryKey: ["agenda", 24 * 14],
    queryFn: () => api.agenda(24 * 14),
  });

  // Events synced from CalDAV/iCal connectors (source_type="event"). Available
  // in both the web and native shells, unlike the native EventKit meetings.
  const syncedEvents = useQuery({
    queryKey: ["synced-events"],
    queryFn: () => {
      const t = Date.now();
      const yr = 365 * 24 * 3600_000;
      return api.agendaEvents(
        new Date(t - yr).toISOString(),
        new Date(t + yr).toISOString(),
      );
    },
  });
  // True total event count for the "N synced" line (the windowed fetch above
  // only returns events within ±1 year).
  const eventCount = useQuery({
    queryKey: ["event-count"],
    queryFn: () => api.knowledgeCount("event"),
  });

  // Whether any calendar connector is configured — drives whether we show the
  // "connect a calendar" help block or a plain "nothing coming up" state.
  const calConnectors = useQuery({
    queryKey: ["connector-instances"],
    queryFn: () => api.connectorInstances(),
  });
  const hasCalendarConnector = (calConnectors.data ?? []).some(
    (i) => i.type_id === "caldav",
  );

  // Short LLM synthesis of upcoming events (header card). Empty when nothing's
  // upcoming; refreshes every 30 min (the endpoint caches ~1h).
  const calSummary = useQuery({
    queryKey: ["calendar-summary"],
    queryFn: () => api.calendarSummary(),
    refetchInterval: 30 * 60_000,
  });

  // Capture "now" once per render — keeps the derivation below free of impure calls.
  const [now] = useState(() => Date.now());
  const allSynced = (syncedEvents.data?.events ?? [])
    .map((it) => {
      const md = (it.metadata ?? {}) as Record<string, unknown>;
      const start = (md.start as string) || it.date || "";
      return {
        content_id: it.content_id,
        title: it.title,
        start,
        location: typeof md.location === "string" ? md.location : "",
        allDay: md.all_day === true,
        ts: start ? Date.parse(start) : NaN,
        body: it.normalized_text ?? "",
      };
    })
    .filter((e) => !Number.isNaN(e.ts));
  const syncedCount = eventCount.data?.total ?? allSynced.length;
  // Upcoming first; if the connected calendar has no future events (e.g. a
  // historical feed), fall back to showing the most recent ones so a synced
  // calendar never looks empty.
  const upcomingSynced = allSynced
    .filter((e) => e.ts >= now - 12 * 3600_000)
    .sort((a, b) => a.ts - b.ts)
    .slice(0, 30);
  const recentSynced = allSynced
    .filter((e) => e.ts < now - 12 * 3600_000)
    .sort((a, b) => b.ts - a.ts)
    .slice(0, 10);
  const renderEventList = (list: typeof allSynced) => (
    <RecordList
      rows={list.map((e) => ({
        id: e.content_id,
        title: e.title,
        meta: e.allDay ? fmtDate(e.start) : fmtDateTime(e.start),
        excerpt: e.location || undefined,
        onClick: () =>
          openDetail({
            title: e.title,
            contentId: e.content_id,
            meta: [
              e.allDay ? fmtDate(e.start) : fmtDateTime(e.start),
              e.location,
            ].filter(Boolean),
            body: e.body,
          }),
      }))}
    />
  );

  const selectedSet = new Set(selected);
  const shownEvents = (events.data ?? []).filter(
    (e) => selected.length === 0 || selectedSet.has(e.calendarId),
  );

  return (
    <Page>
      <PageHeader
        title="Calendar"
        subtitle="Upcoming meetings and the deadlines Hygur extracts from your mail."
        actions={
          isNative && enabled ? (
            <Button
              variant="ghost"
              onClick={() =>
                native.notify(
                  "Hygur",
                  "Notifications are working — you'll get a briefing 30 min before relevant meetings.",
                )
              }
            >
              <Bell size={15} strokeWidth={1.75} />
              Test notification
            </Button>
          ) : undefined
        }
      />

      {/* --- LLM synthesis of what's coming up --- */}
      {calSummary.isLoading ? (
        <div className="mb-7 flex items-center gap-2.5 rounded-xl border border-accent/30 bg-accent-weak/40 px-4 py-3.5 text-[13px] text-accent">
          <Sparkles size={15} strokeWidth={2} className="animate-pulse" />
          Hygur is summarizing what's coming up…
        </div>
      ) : calSummary.data?.summary ? (
        <div className="mb-7 rounded-xl border border-accent/30 bg-accent-weak/40 px-4 py-3">
          <div className="mb-1 flex items-center gap-1.5 text-[11px] font-medium uppercase tracking-wide text-accent">
            <Sparkles size={13} strokeWidth={2} /> What's coming up
          </div>
          <p className="whitespace-pre-line text-[13.5px] leading-relaxed text-text">
            {calSummary.data.summary}
          </p>
        </div>
      ) : null}

      {/* --- Upcoming events (synced CalDAV / iCal calendars) — works in every
           shell (web, cloud, desktop). Primary calendar surface. --- */}
      <SectionLabel>Upcoming events</SectionLabel>

      {syncedEvents.error ? (
        <ErrorBanner
          message={`Couldn't load your calendar: ${(syncedEvents.error as Error).message}`}
          onRetry={() => syncedEvents.refetch()}
        />
      ) : upcomingSynced.length > 0 ? (
        renderEventList(upcomingSynced)
      ) : syncedEvents.isLoading || calConnectors.isLoading ? (
        <Skeleton rows={3} />
      ) : recentSynced.length > 0 ? (
        <>
          <p className="mb-2 text-[13.5px] text-muted">
            Nothing coming up on your calendar. {syncedCount} event
            {syncedCount === 1 ? "" : "s"} synced — the last 10 are below; the rest
            live in your Library.
          </p>
          {renderEventList(recentSynced)}
        </>
      ) : hasCalendarConnector ? (
        <p className="rounded-lg border border-border bg-surface px-4 py-3 text-[13.5px] text-muted">
          Nothing coming up on your calendar.
        </p>
      ) : (
        <div className="rounded-lg border border-border bg-surface px-5 py-6">
          <p className="mb-3 max-w-[58ch] text-[13.5px] text-muted">
            Connect your calendar so its events appear here, become taggable, and
            feed your briefings. Two common ways:
          </p>
          <ul className="mb-4 max-w-[64ch] list-disc space-y-1.5 pl-5 text-[12.5px] text-muted">
            <li>
              <span className="font-medium text-text">Google Calendar</span> — open
              Google Calendar in a browser → Settings → click your calendar →
              “Integrate calendar” → copy the{" "}
              <span className="font-medium">“Secret address in iCal format”</span>{" "}
              link. It's private; no password needed.
            </li>
            <li>
              <span className="font-medium text-text">iCloud</span> — URL{" "}
              <span className="font-medium">https://caldav.icloud.com</span>, Username =
              your Apple ID, Password = an{" "}
              <span className="font-medium">app-specific password</span> (create one at
              appleid.apple.com). Stays private — no public sharing.
            </li>
          </ul>
          <Button onClick={() => navigate("/connectors")}>
            <CalendarClock size={16} strokeWidth={1.75} />
            Connect a calendar
          </Button>
        </div>
      )}

      {/* --- Native EventKit meetings — only when the desktop app exposes the
           native bridge (window.HygurNative). Dormant on the current Tauri build. --- */}
      {isNative && (
        <>
          <SectionLabel>Meetings (native)</SectionLabel>
          {!enabled ? (
            <div className="rounded-lg border border-border bg-surface px-5 py-6">
              <p className="mb-3 max-w-[48ch] text-[13.5px] text-muted">
                Connect your macOS calendars so Hygur can prepare a short briefing
                30 minutes before relevant meetings.
              </p>
              <Button onClick={connect}>
                <CalendarClock size={16} strokeWidth={1.75} />
                Connect calendar
              </Button>
            </div>
          ) : events.isLoading ? (
            <Skeleton rows={3} />
          ) : shownEvents.length > 0 ? (
            <RecordList
              rows={shownEvents.map((e, i) => ({
                id: `${e.title}-${e.start}-${i}`,
                title: e.title,
                meta: e.allDay ? fmtDate(e.start) : fmtDateTime(e.start),
                excerpt:
                  [e.calendarTitle, e.location].filter(Boolean).join(" · ") ||
                  undefined,
              }))}
            />
          ) : (
            <EmptyState
              title="No meetings ahead"
              hint="Nothing scheduled in the next 7 days for the selected calendars."
            />
          )}
        </>
      )}

      {/* --- Calendar selection --- */}
      {isNative && enabled && (calendars.data?.length ?? 0) > 0 && (
        <>
          <SectionLabel>Calendars to watch</SectionLabel>
          <p className="mb-3 text-[13px] text-muted">
            {selected.length === 0
              ? "All calendars are watched. Select specific ones to narrow briefings."
              : `${selected.length} calendar${selected.length === 1 ? "" : "s"} watched.`}
          </p>
          <ToggleGroup
            variant="chips"
            ariaLabel="Calendars to watch"
            value={calendars
              .data!.filter((c) => selected.length === 0 || selectedSet.has(c.id))
              .map((c) => c.id)}
            onChange={toggle}
            options={calendars.data!.map((c) => ({
              value: c.id,
              label: c.title,
              dot: c.color,
            }))}
          />
        </>
      )}

      {/* --- Deadlines from mail (always available) --- */}
      <SectionLabel>Deadlines from your mail</SectionLabel>

      {agenda.error ? (
        <ErrorBanner
          message={`Couldn't load deadlines: ${(agenda.error as Error).message}`}
          onRetry={() => agenda.refetch()}
        />
      ) : agenda.isLoading ? (
        <Skeleton rows={3} />
      ) : (agenda.data?.actions.length ?? 0) > 0 ? (
        <RecordList
          rows={agenda.data!.actions.map((a, i) => ({
            id: `${a.source_id}-${i}`,
            title: a.what,
            meta: fmtDate(a.deadline_iso),
          }))}
        />
      ) : (
        <EmptyState
          title="No upcoming deadlines"
          hint="Hygur surfaces due dates it finds in your mail here."
        />
      )}
    </Page>
  );
}
