import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, ArrowLeft, Trash2 } from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { api } from "../lib/api";
import { fmtDate } from "../lib/format";
import type { Note } from "../lib/types";
import { RecordList, type RecordRow } from "../components/RecordList";
import { TagInput } from "../components/TagInput";
import {
  Button,
  EmptyState,
  ErrorBanner,
  Page,
  PageHeader,
  Skeleton,
  TextInput,
} from "../components/ui";

export function Notes() {
  const qc = useQueryClient();
  const [title, setTitle] = useState("");
  const [editing, setEditing] = useState<Note | null>(null);

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ["notes"],
    queryFn: () => api.notes(),
  });

  const create = useMutation({
    mutationFn: (t: string) => api.createNote(t, `# ${t}\n\n`),
    onSuccess: (note) => {
      setTitle("");
      qc.invalidateQueries({ queryKey: ["notes"] });
      setEditing(note);
    },
  });

  if (editing) {
    return (
      <NoteEditor
        note={editing}
        onClose={() => setEditing(null)}
        onSaved={() => qc.invalidateQueries({ queryKey: ["notes"] })}
        onDeleted={() => {
          qc.invalidateQueries({ queryKey: ["notes"] });
          setEditing(null);
        }}
      />
    );
  }

  const notes = data?.notes ?? [];
  const rows: RecordRow[] = notes.map((n) => ({
    id: n.id,
    title: n.title,
    meta: fmtDate(n.updated_at),
    excerpt: (n.content ?? "").replace(/[#*`>_]/g, "").slice(0, 180),
    onClick: () => setEditing(n),
  }));

  return (
    <Page>
      <PageHeader
        title="Notes"
        subtitle="Your notes — Markdown, editable, indexed for retrieval like everything else."
      />

      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (title.trim()) create.mutate(title.trim());
        }}
        className="mb-5 flex gap-2.5"
      >
        <TextInput
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="New note title…"
        />
        <Button type="submit" disabled={!title.trim() || create.isPending}>
          <Plus size={16} strokeWidth={2} />
          {create.isPending ? "Creating…" : "New note"}
        </Button>
      </form>

      {error && (
        <ErrorBanner
          message={`Couldn't load notes: ${(error as Error).message}`}
          onRetry={() => refetch()}
        />
      )}
      {create.error && (
        <ErrorBanner
          message={`Couldn't create the note: ${(create.error as Error).message}`}
        />
      )}

      {isLoading ? (
        <Skeleton rows={4} />
      ) : rows.length > 0 ? (
        <RecordList rows={rows} />
      ) : (
        <EmptyState
          title="No notes yet"
          hint="Give a note a title above to create your first one."
        />
      )}
    </Page>
  );
}

function NoteEditor({
  note,
  onClose,
  onSaved,
  onDeleted,
}: {
  note: Note;
  onClose: () => void;
  onSaved: () => void;
  onDeleted: () => void;
}) {
  const [title, setTitle] = useState(note.title);
  const [content, setContent] = useState(note.content ?? "");
  const [projectId, setProjectId] = useState(note.project_id ?? "");
  const [tagNames, setTagNames] = useState<string[]>(
    (note.tags ?? []).map((t) => t.name),
  );
  const [tab, setTab] = useState<"edit" | "preview">("edit");
  const [confirmDelete, setConfirmDelete] = useState(false);

  const qc = useQueryClient();
  const projectsQ = useQuery({ queryKey: ["projects"], queryFn: () => api.projects() });
  const tagsQ = useQuery({ queryKey: ["tags"], queryFn: () => api.tags() });

  const save = useMutation({
    mutationFn: async () => {
      const tagIds = await api.resolveTagIds(tagNames, tagsQ.data?.tags ?? []);
      qc.invalidateQueries({ queryKey: ["tags"] });
      return api.updateNote(note.id, {
        title,
        content,
        project_id: projectId,
        tag_ids: tagIds,
      });
    },
    onSuccess: () => {
      onSaved();
      onClose();
    },
  });

  const remove = useMutation({
    mutationFn: () => api.deleteNote(note.id),
    onSuccess: onDeleted,
  });

  const dirty =
    title !== note.title ||
    content !== (note.content ?? "") ||
    projectId !== (note.project_id ?? "") ||
    tagNames.join(",") !== (note.tags ?? []).map((t) => t.name).join(",");

  return (
    <Page>
      <div className="mb-5 flex items-center justify-between gap-3">
        <button
          onClick={onClose}
          className="inline-flex items-center gap-1.5 text-[13.5px] text-muted transition-colors hover:text-text"
        >
          <ArrowLeft size={16} strokeWidth={1.75} /> Notes
        </button>
        <div className="flex items-center gap-2">
          {confirmDelete ? (
            <>
              <span className="text-[12.5px] text-muted">Delete?</span>
              <Button variant="ghost" onClick={() => remove.mutate()}>
                {remove.isPending ? "Deleting…" : "Yes, delete"}
              </Button>
              <Button variant="ghost" onClick={() => setConfirmDelete(false)}>
                Cancel
              </Button>
            </>
          ) : (
            <button
              onClick={() => setConfirmDelete(true)}
              aria-label="Delete note"
              className="rounded-md p-1.5 text-muted transition-colors hover:bg-danger/10 hover:text-danger"
            >
              <Trash2 size={17} strokeWidth={1.75} />
            </button>
          )}
          <Button onClick={() => save.mutate()} disabled={!dirty || save.isPending}>
            {save.isPending ? "Saving…" : "Save"}
          </Button>
        </div>
      </div>

      {(save.error || remove.error) && (
        <ErrorBanner
          message={`Couldn't save: ${((save.error || remove.error) as Error).message}`}
        />
      )}

      <input
        value={title}
        onChange={(e) => setTitle(e.target.value)}
        placeholder="Untitled"
        className="mb-4 w-full bg-transparent font-display text-[24px] font-semibold leading-tight tracking-tight outline-none placeholder:text-faint"
      />

      <div className="mb-3 inline-flex rounded-lg border border-border bg-surface p-0.5 text-[12.5px]">
        {(["edit", "preview"] as const).map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`rounded-md px-3 py-1 capitalize transition-colors ${
              tab === t ? "bg-accent-weak font-medium text-accent" : "text-muted hover:text-text"
            }`}
          >
            {t}
          </button>
        ))}
      </div>

      {tab === "edit" ? (
        <textarea
          value={content}
          onChange={(e) => setContent(e.target.value)}
          placeholder="Write in Markdown…"
          className="min-h-[50vh] w-full resize-y rounded-xl border border-border bg-surface px-4 py-3 font-mono text-[13.5px] leading-relaxed text-text outline-none transition-colors focus:border-accent"
        />
      ) : (
        <div className="prose-answer min-h-[50vh] rounded-xl border border-border bg-surface px-5 py-4 text-[14.5px] leading-relaxed">
          <ReactMarkdown remarkPlugins={[remarkGfm]}>
            {content || "_(nothing to preview)_"}
          </ReactMarkdown>
        </div>
      )}

      {/* Project + tags at the bottom — attach the note and tag it. */}
      <div className="mt-5 border-t border-border pt-4">
        <div className="mb-3 flex items-center gap-2.5">
          <span className="w-16 text-[12.5px] text-muted">Project</span>
          <select
            value={projectId}
            onChange={(e) => setProjectId(e.target.value)}
            className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-[13px] outline-none focus:border-accent"
          >
            <option value="">No project</option>
            {(projectsQ.data ?? []).map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </div>
        <div className="flex items-start gap-2.5">
          <span className="w-16 shrink-0 pt-2 text-[12.5px] text-muted">Tags</span>
          <div className="flex-1">
            <TagInput
              value={tagNames}
              suggestions={(tagsQ.data?.tags ?? []).map((t) => t.name)}
              onChange={setTagNames}
            />
          </div>
        </div>
      </div>
    </Page>
  );
}
