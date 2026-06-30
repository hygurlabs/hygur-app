import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { BookOpen, CalendarClock, CheckSquare, Compass, Gavel, Scale } from "lucide-react";
import { api } from "../lib/api";
import { fmtDate } from "../lib/format";
import { EmptyState, Page, PageHeader, Skeleton } from "../components/ui";

/** The daily "state of your world": Hygur assembles what it already knows —
 *  where things stand, what's contradictory, what needs a decision, what's due —
 *  into one calm surface. Composition, not generation: each row links to its
 *  full view. */
export function Digest() {
  const { data, isLoading } = useQuery({ queryKey: ["digest"], queryFn: () => api.digest() });

  const synopsis = data?.synopsis?.trim() ?? "";
  const positions = data?.positions?.trim() ?? "";
  const contradictions = data?.contradictions ?? [];
  const decisions = data?.proposed_decisions ?? [];
  const tasks = data?.due_tasks ?? [];
  const upcoming = data?.upcoming ?? [];
  const nothing =
    !synopsis &&
    !positions &&
    contradictions.length === 0 &&
    decisions.length === 0 &&
    tasks.length === 0 &&
    upcoming.length === 0;

  return (
    <Page>
      <PageHeader
        title="Aujourd’hui"
        subtitle="Voici où en sont les choses, ce qui reste ouvert, et ce qui demande votre attention aujourd’hui."
      />

      {isLoading ? (
        <Skeleton rows={6} />
      ) : nothing ? (
        <EmptyState
          title="Rien ne requiert votre attention pour l’instant"
          hint="Connectez vos mails et notes — une fois que Hygur les a lus, Aujourd’hui devient votre point de départ du matin : ce qui a bougé, ce qui est ouvert, et ce qui demande votre attention."
        />
      ) : (
        <div className="mx-auto flex max-w-[680px] flex-col gap-6">
          {synopsis && (
            <Section icon={BookOpen} title="Où en sont les choses" to="/chronicle">
              <div className="prose-answer font-display text-[15px] leading-[1.7] text-text [&_p]:mb-2">
                <ReactMarkdown remarkPlugins={[remarkGfm]}>{synopsis}</ReactMarkdown>
              </div>
            </Section>
          )}

          {positions && (
            <Section icon={Compass} title="Où vous en êtes" to="/decisions">
              <div className="prose-answer font-display text-[15px] leading-[1.7] text-text [&_p]:mb-2">
                <ReactMarkdown remarkPlugins={[remarkGfm]}>{positions}</ReactMarkdown>
              </div>
            </Section>
          )}

          {upcoming.length > 0 && (
            <Section icon={CalendarClock} title={`À venir · ${upcoming.length}`}>
              <ul className="flex flex-col gap-2">
                {upcoming.map((u, i) => (
                  <li
                    key={`${u.title}-${u.at}-${i}`}
                    className="flex items-baseline justify-between gap-3 text-[14px]"
                  >
                    <span className="text-text">{u.title}</span>
                    <span className="tnum shrink-0 text-[12px] text-muted">
                      ~{fmtDate(u.at)} · {u.detail}
                    </span>
                  </li>
                ))}
              </ul>
            </Section>
          )}

          {decisions.length > 0 && (
            <Section
              icon={Gavel}
              title={`Décisions à confirmer · ${decisions.length}`}
              to="/decisions"
            >
              <ul className="flex flex-col gap-2">
                {decisions.map((d) => (
                  <li key={d.id} className="text-[14px] text-text">
                    {d.statement}
                    {d.decided_on && (
                      <span className="tnum ml-2 text-[12px] text-muted">{fmtDate(d.decided_on)}</span>
                    )}
                  </li>
                ))}
              </ul>
            </Section>
          )}

          {contradictions.length > 0 && (
            <Section
              icon={Scale}
              title={`Contradictions ouvertes · ${contradictions.length}`}
              to="/contradictions"
            >
              <ul className="flex flex-col gap-2.5">
                {contradictions.map((c) => (
                  <li key={c.key} className="text-[13.5px]">
                    <span className="text-text">
                      {c.entity && <span className="font-medium">{c.entity} — </span>}
                      {c.attribute}
                    </span>
                    <span className="mt-0.5 block text-[12.5px] text-muted">
                      {c.members.map((m) => m.value).filter(Boolean).join("  vs  ")}
                      {c.verdict?.reason ? ` — ${c.verdict.reason}` : ""}
                    </span>
                  </li>
                ))}
              </ul>
            </Section>
          )}

          {tasks.length > 0 && (
            <Section icon={CheckSquare} title={`Échéance proche · ${tasks.length}`} to="/tasks">
              <ul className="flex flex-col gap-2">
                {tasks.map((t) => (
                  <li key={t.id} className="flex items-baseline justify-between gap-3 text-[14px]">
                    <span className="text-text">{t.title}</span>
                    {t.due_date && (
                      <span className="tnum shrink-0 text-[12px] text-muted">
                        {fmtDate(t.due_date)}
                      </span>
                    )}
                  </li>
                ))}
              </ul>
            </Section>
          )}
        </div>
      )}
    </Page>
  );
}

function Section({
  icon: Icon,
  title,
  to,
  children,
}: {
  icon: typeof BookOpen;
  title: string;
  to?: string;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-xl border border-border bg-surface p-5">
      <div className="mb-3 flex items-center justify-between">
        <h2 className="flex items-center gap-2 text-[12px] font-medium uppercase tracking-[0.09em] text-faint">
          <Icon size={14} strokeWidth={1.9} />
          {title}
        </h2>
        {to && (
          <Link to={to} className="text-[12.5px] text-accent transition-colors hover:underline">
            Ouvrir
          </Link>
        )}
      </div>
      {children}
    </section>
  );
}
