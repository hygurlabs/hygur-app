import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2, Check, X } from "lucide-react";
import { api } from "../lib/api";
import { useToast } from "../lib/toast";
import { RecordList } from "../components/RecordList";
import {
  Badge,
  Button,
  EmptyState,
  ErrorBanner,
  Page,
  PageHeader,
  Skeleton,
  TextInput,
} from "../components/ui";

const TYPES = ["fact", "preference", "action"] as const;

// Display labels — the value sent to the backend stays the wire contract
// ("fact" | "preference" | "action", validated server-side); only the wording
// shown to the user changes. "action" was the confusing one.
const TYPE_LABELS: Record<string, string> = {
  fact: "Fact",
  preference: "Preference",
  action: "Recurring action",
};
const typeLabel = (t: string) => TYPE_LABELS[t] ?? t;

export function MemoryView() {
  const qc = useQueryClient();
  const toast = useToast();
  const invalidate = () => qc.invalidateQueries({ queryKey: ["memories"] });

  const list = useQuery({ queryKey: ["memories", "list"], queryFn: () => api.memories() });
  const pending = useQuery({ queryKey: ["memories", "pending"], queryFn: () => api.pendingMemories() });

  const [type, setType] = useState<string>("fact");
  const [content, setContent] = useState("");
  // Per-row delete confirm (the inline pattern used in Notes/Tags).
  const [confirmId, setConfirmId] = useState<string | null>(null);

  const add = useMutation({
    mutationFn: () => api.storeMemory({ type, content: content.trim() }),
    onSuccess: () => {
      setContent("");
      invalidate();
    },
    onError: (e) => toast.error(`Couldn't add that: ${(e as Error).message}`),
  });
  const accept = useMutation({
    mutationFn: (id: string) => api.acceptMemory(id),
    onSuccess: () => {
      invalidate();
      toast.success("Memory kept.");
    },
    onError: (e) => toast.error(`Couldn't keep that: ${(e as Error).message}`),
  });
  const discard = useMutation({
    mutationFn: (id: string) => api.discardMemory(id),
    onSuccess: invalidate,
    onError: (e) => toast.error(`Couldn't discard that: ${(e as Error).message}`),
  });
  const remove = useMutation({
    mutationFn: (id: string) => api.deleteMemory(id),
    onSuccess: () => {
      setConfirmId(null);
      invalidate();
    },
    onError: (e) => toast.error(`Couldn't forget that: ${(e as Error).message}`),
  });

  const memories = list.data?.memories ?? [];
  const pend = pending.data?.memories ?? [];

  return (
    <Page>
      <PageHeader
        title="Memory"
        subtitle="What Hygur remembers about you — facts, preferences and recurring actions it folds into its answers. Review what it learns, or add your own."
      />

      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (content.trim()) add.mutate();
        }}
        className="mb-6 flex flex-wrap gap-2"
      >
        <select
          value={type}
          onChange={(e) => setType(e.target.value)}
          aria-label="Memory type"
          className="rounded-lg border border-border bg-surface px-2.5 py-2 text-[13px] outline-none focus:border-accent"
        >
          {TYPES.map((t) => (
            <option key={t} value={t}>
              {typeLabel(t)}
            </option>
          ))}
        </select>
        <TextInput
          value={content}
          onChange={(e) => setContent(e.target.value)}
          placeholder="Something Hygur should remember…"
          className="min-w-[220px] flex-1"
        />
        <Button type="submit" disabled={!content.trim() || add.isPending}>
          <Plus size={16} strokeWidth={2} />
          {add.isPending ? "Adding…" : "Add"}
        </Button>
      </form>

      {(list.error || pending.error) && (
        <ErrorBanner
          message="Couldn't load memories."
          onRetry={() => {
            list.refetch();
            pending.refetch();
          }}
        />
      )}
      {pend.length > 0 && (
        <section className="mb-7">
          <h2 className="mb-2.5 text-[11.5px] font-medium uppercase tracking-[0.09em] text-faint">
            To review ({pend.length})
          </h2>
          <RecordList
            variant="card"
            accent
            align="start"
            rows={pend.map((m) => ({
              id: m.memory_id,
              content: (
                <>
                  <div className="mb-1">
                    <Badge>{typeLabel(m.type)}</Badge>
                  </div>
                  <p className="text-[14px] text-text">{m.content}</p>
                </>
              ),
              trailing: (
                <div className="flex shrink-0 gap-1.5">
                  <button
                    onClick={() => accept.mutate(m.memory_id)}
                    aria-label="Keep"
                    title="Keep"
                    className="grid size-8 place-items-center rounded-lg border border-border text-muted transition-colors hover:border-accent hover:text-accent"
                  >
                    <Check size={15} strokeWidth={2} />
                  </button>
                  <button
                    onClick={() => discard.mutate(m.memory_id)}
                    aria-label="Discard"
                    title="Discard"
                    className="grid size-8 place-items-center rounded-lg border border-border text-muted transition-colors hover:bg-danger/10 hover:text-danger"
                  >
                    <X size={15} strokeWidth={2} />
                  </button>
                </div>
              ),
            }))}
          />
        </section>
      )}

      {list.isLoading ? (
        <Skeleton rows={4} />
      ) : memories.length > 0 ? (
        <RecordList
          variant="card"
          align="start"
          rows={memories.map((m) => ({
            id: m.memory_id,
            content: (
              <>
                <div className="mb-1 flex items-center gap-2">
                  <Badge>{typeLabel(m.type)}</Badge>
                  {m.source === "extracted" && (
                    <span className="text-[11px] text-faint">learned</span>
                  )}
                </div>
                <p className="text-[14px] text-text">{m.content}</p>
              </>
            ),
            trailing:
              confirmId === m.memory_id ? (
                <span className="flex shrink-0 items-center gap-1.5 text-[12.5px]">
                  <span className="text-muted">Delete?</span>
                  <button
                    onClick={() => remove.mutate(m.memory_id)}
                    disabled={remove.isPending}
                    className="rounded-md px-2 py-0.5 font-medium text-danger transition-colors hover:bg-danger/10"
                  >
                    {remove.isPending ? "…" : "Yes"}
                  </button>
                  <button
                    onClick={() => setConfirmId(null)}
                    className="rounded-md px-2 py-0.5 text-muted transition-colors hover:bg-surface2 hover:text-text"
                  >
                    Cancel
                  </button>
                </span>
              ) : (
                <button
                  onClick={() => setConfirmId(m.memory_id)}
                  aria-label="Forget"
                  title="Forget"
                  className="shrink-0 rounded-md p-1.5 text-faint opacity-0 transition-all hover:bg-danger/10 hover:text-danger focus:opacity-100 group-hover:opacity-100"
                >
                  <Trash2 size={15} strokeWidth={1.75} />
                </button>
              ),
          }))}
        />
      ) : pend.length === 0 ? (
        <EmptyState
          title="Nothing remembered yet"
          hint="Hygur learns facts and preferences as you use it — accept them here, or add your own above."
        />
      ) : null}
    </Page>
  );
}
