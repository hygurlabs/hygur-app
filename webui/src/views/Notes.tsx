import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Plus,
  ArrowLeft,
  Trash2,
  Bold,
  Italic,
  Heading2,
  List,
  ListOrdered,
  Quote,
  Code,
  Link as LinkIcon,
} from "lucide-react";
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

// Static toolbar config (module scope so render builds no ref-capturing
// closures). The component maps a key → formatting action.
const NOTE_TOOLBAR = [
  { key: "bold", icon: Bold, label: "Gras" },
  { key: "italic", icon: Italic, label: "Italique" },
  { key: "h2", icon: Heading2, label: "Titre" },
  { key: "ul", icon: List, label: "Liste" },
  { key: "ol", icon: ListOrdered, label: "Liste numérotée" },
  { key: "quote", icon: Quote, label: "Citation" },
  { key: "code", icon: Code, label: "Code" },
  { key: "link", icon: LinkIcon, label: "Lien" },
] as const;

export function Notes() {
  const qc = useQueryClient();
  const [title, setTitle] = useState("");
  const [editing, setEditing] = useState<Note | null>(null);

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ["notes"],
    queryFn: () => api.notes(),
  });
  const projectsQ = useQuery({ queryKey: ["projects"], queryFn: () => api.projects() });
  const projectName = (id?: string | null) =>
    id ? projectsQ.data?.find((p) => p.id === id)?.name : undefined;

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
    projectName: projectName(n.project_id),
    tags: (n.tags ?? []).map((t) => ({ name: t.name, color: t.color })),
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
  const [view, setView] = useState<"split" | "write" | "preview">("split");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const taRef = useRef<HTMLTextAreaElement>(null);

  // Restores the textarea selection after a state-driven value change so the
  // cursor lands where the user expects post-formatting.
  function selectAfterRender(start: number, end: number) {
    requestAnimationFrame(() => {
      const ta = taRef.current;
      if (!ta) return;
      ta.focus();
      ta.setSelectionRange(start, end);
    });
  }

  // Wraps the current selection (or a placeholder) with Markdown delimiters.
  function surround(before: string, after = before, placeholder = "texte") {
    const ta = taRef.current;
    if (!ta) return;
    const { selectionStart: s, selectionEnd: e } = ta;
    const sel = content.slice(s, e) || placeholder;
    setContent(content.slice(0, s) + before + sel + after + content.slice(e));
    selectAfterRender(s + before.length, s + before.length + sel.length);
  }

  // Prefixes each selected line (headings, lists, quotes).
  function prefixLines(prefix: string) {
    const ta = taRef.current;
    if (!ta) return;
    const { selectionStart: s, selectionEnd: e } = ta;
    const lineStart = content.lastIndexOf("\n", s - 1) + 1;
    const block = content.slice(lineStart, e) || "texte";
    const replaced = block.replace(/^/gm, prefix);
    setContent(content.slice(0, lineStart) + replaced + content.slice(e));
    selectAfterRender(lineStart, lineStart + replaced.length);
  }

  function insertLink() {
    const ta = taRef.current;
    if (!ta) return;
    const { selectionStart: s, selectionEnd: e } = ta;
    const sel = content.slice(s, e) || "texte";
    const snippet = `[${sel}](url)`;
    setContent(content.slice(0, s) + snippet + content.slice(e));
    const urlStart = s + sel.length + 3; // after "[sel]("
    selectAfterRender(urlStart, urlStart + 3);
  }

  // Dispatches a toolbar action by key. Defined here (not as inline closures in
  // the rendered array) so render never builds ref-capturing functions.
  function applyFormat(key: string) {
    switch (key) {
      case "bold":
        return surround("**");
      case "italic":
        return surround("*");
      case "h2":
        return prefixLines("## ");
      case "ul":
        return prefixLines("- ");
      case "ol":
        return prefixLines("1. ");
      case "quote":
        return prefixLines("> ");
      case "code":
        return surround("`", "`", "code");
      case "link":
        return insertLink();
    }
  }

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

      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        {/* Formatting toolbar — hidden in preview-only mode. */}
        <div
          className={`flex items-center gap-0.5 rounded-lg border border-border bg-surface p-0.5 ${
            view === "preview" ? "pointer-events-none opacity-40" : ""
          }`}
        >
          {NOTE_TOOLBAR.map(({ key, icon: Icon, label }) => (
            <button
              key={key}
              type="button"
              onMouseDown={(e) => e.preventDefault()} // keep textarea selection
              onClick={() => applyFormat(key)}
              title={label}
              aria-label={label}
              className="grid size-7 place-items-center rounded-md text-muted transition-colors hover:bg-surface2 hover:text-text"
            >
              <Icon size={15} strokeWidth={1.9} />
            </button>
          ))}
        </div>

        <div className="inline-flex rounded-lg border border-border bg-surface p-0.5 text-[12.5px]">
          {(
            [
              ["split", "Split"],
              ["write", "Éditeur"],
              ["preview", "Aperçu"],
            ] as const
          ).map(([v, label]) => (
            <button
              key={v}
              onClick={() => setView(v)}
              className={`rounded-md px-3 py-1 transition-colors ${
                view === v
                  ? "bg-accent-weak font-medium text-accent"
                  : "text-muted hover:text-text"
              }`}
            >
              {label}
            </button>
          ))}
        </div>
      </div>

      <div
        className={
          view === "split"
            ? "grid min-h-[50vh] grid-cols-1 gap-3 lg:grid-cols-2"
            : "min-h-[50vh]"
        }
      >
        {view !== "preview" && (
          <textarea
            ref={taRef}
            value={content}
            onChange={(e) => setContent(e.target.value)}
            placeholder="Write in Markdown…"
            className="min-h-[50vh] w-full resize-y rounded-xl border border-border bg-surface px-4 py-3 font-mono text-[13.5px] leading-relaxed text-text outline-none transition-colors focus:border-accent"
          />
        )}
        {view !== "write" && (
          <div className="prose-answer min-h-[50vh] overflow-auto rounded-xl border border-border bg-surface px-5 py-4 text-[14.5px] leading-relaxed">
            <ReactMarkdown remarkPlugins={[remarkGfm]}>
              {content || "_(rien à prévisualiser)_"}
            </ReactMarkdown>
          </div>
        )}
      </div>

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
