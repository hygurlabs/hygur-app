import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, ArrowLeft, Trash2, X, FolderOpen } from "lucide-react";
import { api } from "../lib/api";
import { fmtDate, srcLabel } from "../lib/format";
import { useToast } from "../lib/toast";
import type { Project } from "../lib/types";
import { useDetail } from "../components/DetailPanel";
import { RecordList, type RecordRow } from "../components/RecordList";
import { TagInput } from "../components/TagInput";
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

export function Projects() {
  const qc = useQueryClient();
  const toast = useToast();
  const [name, setName] = useState("");
  const [openId, setOpenId] = useState<string | null>(null);

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ["projects"],
    queryFn: () => api.projects(),
  });

  const create = useMutation({
    mutationFn: (n: string) => api.createProject(n),
    onSuccess: (p) => {
      setName("");
      qc.invalidateQueries({ queryKey: ["projects"] });
      setOpenId(p.id);
      toast.success("Project created.");
    },
    onError: (e) => toast.error(`Couldn't create the project: ${(e as Error).message}`),
  });

  if (openId) {
    return <ProjectDetail id={openId} onClose={() => setOpenId(null)} />;
  }

  const projects = data ?? [];
  const rows: RecordRow[] = projects.map((p) => ({
    id: p.id,
    title: p.name,
    badge: `${p.item_count} item${p.item_count === 1 ? "" : "s"}`,
    meta: fmtDate(p.updated_at),
    excerpt: p.description || undefined,
    onClick: () => setOpenId(p.id),
  }));

  return (
    <Page>
      <PageHeader
        title="Projects"
        subtitle="Group notes, mails and documents — and scope a chat to one with @."
      />

      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (name.trim()) create.mutate(name.trim());
        }}
        className="mb-5 flex gap-2.5"
      >
        <TextInput
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="New project name…"
        />
        <Button type="submit" disabled={!name.trim() || create.isPending}>
          <Plus size={16} strokeWidth={2} />
          {create.isPending ? "Creating…" : "New project"}
        </Button>
      </form>

      {error && (
        <ErrorBanner
          message={`Couldn't load projects: ${(error as Error).message}`}
          onRetry={() => refetch()}
        />
      )}

      {isLoading ? (
        <Skeleton rows={4} />
      ) : rows.length > 0 ? (
        <RecordList rows={rows} />
      ) : (
        <EmptyState
          title="No projects yet"
          hint="Create one above, then link notes, mails and documents to it."
        />
      )}
    </Page>
  );
}

function ProjectDetail({ id, onClose }: { id: string; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const openDetail = useDetail();
  const [confirmDelete, setConfirmDelete] = useState(false);

  const projectQ = useQuery({
    queryKey: ["projects"],
    queryFn: () => api.projects(),
  });
  const project: Project | undefined = projectQ.data?.find((p) => p.id === id);

  const itemsQ = useQuery({
    queryKey: ["project-items", id],
    queryFn: () => api.projectItems(id),
  });

  // The project may not be in the cached list on first render (e.g. just
  // created). Hydrate the fields once it arrives rather than initialising from
  // an undefined project (which left "Project name" blank).
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [tags, setTags] = useState<string[]>([]);
  const [noteOpen, setNoteOpen] = useState(false);
  const [noteTitle, setNoteTitle] = useState("");
  const [noteBody, setNoteBody] = useState("");
  const hydrated = useRef(false);
  useEffect(() => {
    if (project && !hydrated.current) {
      setName(project.name);
      setDescription(project.description ?? "");
      setTags(project.tags ?? []);
      hydrated.current = true;
    }
  }, [project]);

  const tagsQ = useQuery({ queryKey: ["tags"], queryFn: () => api.tags() });

  const save = useMutation({
    mutationFn: () => api.updateProject(id, { name, description, tags }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["projects"] }),
    onError: (e) => toast.error(`Couldn't save the project: ${(e as Error).message}`),
  });
  const remove = useMutation({
    mutationFn: () => api.deleteProject(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["projects"] });
      onClose();
    },
    onError: (e) => toast.error(`Couldn't delete the project: ${(e as Error).message}`),
  });
  const unlink = useMutation({
    mutationFn: (contentId: string) => api.unlinkItemFromProject(contentId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["project-items", id] });
      qc.invalidateQueries({ queryKey: ["projects"] });
    },
    onError: (e) => toast.error(`Couldn't unlink the item: ${(e as Error).message}`),
  });
  // Create a note and link it to this project in one step.
  const addNote = useMutation({
    mutationFn: async () => {
      const note = await api.createNote(noteTitle.trim() || "New note", noteBody.trim());
      await api.linkItemToProject(note.id, id);
    },
    onSuccess: () => {
      setNoteOpen(false);
      setNoteTitle("");
      setNoteBody("");
      qc.invalidateQueries({ queryKey: ["project-items", id] });
      qc.invalidateQueries({ queryKey: ["notes"] });
    },
    onError: (e) => toast.error(`Couldn't add the note: ${(e as Error).message}`),
  });

  async function openItem(contentId: string, title: string, sourceType: string) {
    try {
      const item = await api.knowledgeItem(contentId);
      openDetail({
        title: item.title || title,
        contentId,
        sourceType: item.source_type,
        meta: [srcLabel(item.source_type)],
        body: item.normalized_text ?? "",
      });
    } catch {
      openDetail({
        title,
        contentId,
        sourceType,
        meta: [srcLabel(sourceType)],
        body: "_(could not load this item)_",
      });
    }
  }

  const items = itemsQ.data?.items ?? [];
  const dirty =
    project &&
    (name !== project.name ||
      description !== (project.description ?? "") ||
      tags.join(",") !== (project.tags ?? []).join(","));

  return (
    <Page>
      <div className="mb-5 flex items-center justify-between gap-3">
        <button
          onClick={onClose}
          className="inline-flex items-center gap-1.5 text-[13.5px] text-muted transition-colors hover:text-text"
        >
          <ArrowLeft size={16} strokeWidth={1.75} /> Projects
        </button>
        <div className="flex items-center gap-2">
          {confirmDelete ? (
            <>
              <span className="text-[12.5px] text-muted">Delete project?</span>
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
              aria-label="Delete project"
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

      <input
        value={name}
        onChange={(e) => setName(e.target.value)}
        placeholder="Project name"
        className="mb-2 w-full bg-transparent font-display text-[24px] font-semibold leading-tight tracking-tight outline-none placeholder:text-faint"
      />
      <textarea
        value={description}
        onChange={(e) => setDescription(e.target.value)}
        placeholder="A short description…"
        rows={2}
        className="mb-4 w-full resize-y bg-transparent text-[14px] text-muted outline-none placeholder:text-faint"
      />

      <div className="mb-6">
        <span className="mb-1.5 block text-[11.5px] font-medium uppercase tracking-[0.09em] text-faint">
          Tags
        </span>
        <TagInput
          value={tags}
          suggestions={(tagsQ.data?.tags ?? []).map((t) => t.name)}
          onChange={setTags}
        />
      </div>

      <div className="mb-1 flex items-center justify-between gap-3">
        <p className="text-[11.5px] font-medium uppercase tracking-[0.09em] text-faint">
          Linked items
        </p>
        <button
          onClick={() => setNoteOpen((v) => !v)}
          className="inline-flex items-center gap-1 text-[12.5px] font-medium text-accent transition-colors hover:underline"
        >
          <Plus size={14} strokeWidth={2} /> New note
        </button>
      </div>
      {noteOpen && (
        <div className="mb-3 rounded-lg border border-border bg-surface p-3">
          <input
            value={noteTitle}
            onChange={(e) => setNoteTitle(e.target.value)}
            placeholder="Note title"
            autoFocus
            className="mb-2 w-full bg-transparent text-[14px] font-medium outline-none placeholder:text-faint"
          />
          <textarea
            value={noteBody}
            onChange={(e) => setNoteBody(e.target.value)}
            placeholder="Write your note…"
            rows={3}
            className="w-full resize-y bg-transparent text-[13.5px] leading-relaxed outline-none placeholder:text-faint"
          />
          <div className="mt-2 flex items-center gap-2">
            <Button
              onClick={() => addNote.mutate()}
              disabled={addNote.isPending || (!noteTitle.trim() && !noteBody.trim())}
            >
              {addNote.isPending ? "Creating…" : "Create note"}
            </Button>
            <Button
              variant="ghost"
              onClick={() => {
                setNoteOpen(false);
                setNoteTitle("");
                setNoteBody("");
              }}
            >
              Cancel
            </Button>
          </div>
        </div>
      )}
      {itemsQ.isLoading ? (
        <Skeleton rows={3} />
      ) : items.length === 0 ? (
        <EmptyState
          title="Nothing linked yet"
          hint="Open a note, mail or document and link it to this project."
        />
      ) : (
        <RecordList
          rows={items.map((it) => ({
            id: it.id,
            content: (
              <button
                onClick={() => openItem(it.id, it.title, it.source_type)}
                className="flex w-full min-w-0 items-center gap-2 text-left transition-colors hover:text-accent"
              >
                <FolderOpen size={15} strokeWidth={1.75} className="shrink-0 text-faint" />
                <span className="truncate font-medium">{it.title || "(untitled)"}</span>
                <Badge>{srcLabel(it.source_type)}</Badge>
              </button>
            ),
            trailing: (
              <button
                onClick={() => unlink.mutate(it.id)}
                aria-label="Unlink"
                className="shrink-0 rounded-md p-1 text-faint transition-colors hover:bg-surface2 hover:text-danger"
              >
                <X size={15} strokeWidth={1.75} />
              </button>
            ),
          }))}
        />
      )}
    </Page>
  );
}
