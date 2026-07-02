import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Plus, Check, Trash2, FolderKanban, X, CalendarClock } from "lucide-react";
import { api } from "../lib/api";
import { fmtDate } from "../lib/format";
import { useToast } from "../lib/toast";
import type { Task } from "../lib/types";
import { TagInput } from "../components/TagInput";
import { RecordList } from "../components/RecordList";
import { Button, EmptyState, ErrorBanner, Skeleton, TextInput } from "../components/ui";

export function Tasks() {
  const qc = useQueryClient();
  const toast = useToast();
  const [title, setTitle] = useState("");
  const [selectedId, setSelectedId] = useState<string | null>(null);
  // Per-row delete confirm (the inline pattern used in Notes/Tags).
  const [confirmId, setConfirmId] = useState<string | null>(null);

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ["tasks"],
    queryFn: () => api.tasks(),
  });
  const projectsQ = useQuery({ queryKey: ["projects"], queryFn: () => api.projects() });
  const projectName = (id?: string) =>
    id ? projectsQ.data?.find((p) => p.id === id)?.name : undefined;

  const invalidate = () => qc.invalidateQueries({ queryKey: ["tasks"] });

  const create = useMutation({
    mutationFn: (t: string) => api.createTask({ title: t }),
    onSuccess: (task) => {
      setTitle("");
      invalidate();
      setSelectedId(task.id); // open the new task for body/tags/project/due
      toast.success("Task created.");
    },
    onError: (e) => toast.error(`Couldn't add the task: ${(e as Error).message}`),
  });
  const toggle = useMutation({
    mutationFn: (t: Task) =>
      api.updateTask(t.id, { status: t.status === "done" ? "open" : "done" }),
    onSuccess: invalidate,
    onError: (e) => toast.error(`Couldn't update the task: ${(e as Error).message}`),
  });
  const remove = useMutation({
    mutationFn: (id: string) => api.deleteTask(id),
    onSuccess: (_d, id) => {
      setConfirmId(null);
      invalidate();
      if (selectedId === id) setSelectedId(null);
    },
    onError: (e) => toast.error(`Couldn't delete the task: ${(e as Error).message}`),
  });

  // Server orders open-first, due-soonest, newest.
  const tasks = data?.tasks ?? [];
  const openCount = tasks.filter((t) => t.status !== "done").length;
  const selected = tasks.find((t) => t.id === selectedId) ?? null;

  return (
    <div className="flex h-full">
      <div className="flex min-w-0 flex-1 flex-col overflow-y-auto">
        <div className="mx-auto w-full max-w-[760px] px-4 pb-24 pt-9 sm:px-7">
          <header className="mb-7">
            <h1 className="font-display text-[26px] font-semibold leading-tight tracking-tight">
              Tasks
            </h1>
            <p className="mt-1 max-w-[64ch] text-[13.5px] text-muted">
              Note-like to-dos: a Markdown body, tags and a project, with a due date Hygur
              surfaces in your briefings. Click a task to edit it.
            </p>
          </header>

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
              placeholder="New task…"
            />
            <Button type="submit" disabled={!title.trim() || create.isPending}>
              <Plus size={16} strokeWidth={2} />
              {create.isPending ? "Adding…" : "Add"}
            </Button>
          </form>

          {error && (
            <ErrorBanner
              message={`Couldn't load tasks: ${(error as Error).message}`}
              onRetry={() => refetch()}
            />
          )}
          {isLoading ? (
            <Skeleton rows={4} />
          ) : tasks.length > 0 ? (
            <>
              <div className="mb-2 text-[12.5px] text-muted">
                {openCount} open · {tasks.length - openCount} done
              </div>
              <RecordList
                variant="card"
                rows={tasks.map((t) => {
                  const done = t.status === "done";
                  const proj = projectName(t.project_id);
                  return {
                    id: t.id,
                    selected: selectedId === t.id,
                    leading: (
                      <button
                        onClick={() => toggle.mutate(t)}
                        aria-label={done ? "Mark as open" : "Mark as done"}
                        className={`grid size-[18px] shrink-0 place-items-center rounded-md border transition-colors ${
                          done
                            ? "border-accent bg-accent text-white"
                            : "border-border text-transparent hover:border-accent"
                        }`}
                      >
                        <Check size={12} strokeWidth={2.5} />
                      </button>
                    ),
                    content: (
                      <button
                        onClick={() => setSelectedId(t.id)}
                        className="w-full min-w-0 text-left"
                      >
                        <p
                          className={`truncate text-[14px] ${
                            done ? "text-muted line-through" : "text-text"
                          }`}
                        >
                          {t.title}
                        </p>
                        {(proj || t.due_date || t.tags.length > 0) && (
                          <div className="mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[12px] text-muted">
                            {proj && (
                              <span className="inline-flex items-center gap-1">
                                <FolderKanban size={12} strokeWidth={1.75} />
                                {proj}
                              </span>
                            )}
                            {t.due_date && <span className="tnum">due {fmtDate(t.due_date)}</span>}
                            {t.tags.length > 0 && (
                              <span className="truncate">{t.tags.map((tag) => tag.name).join(", ")}</span>
                            )}
                          </div>
                        )}
                      </button>
                    ),
                    trailing:
                      confirmId === t.id ? (
                        <span className="flex shrink-0 items-center gap-1.5 text-[12.5px]">
                          <span className="text-muted">Delete?</span>
                          <button
                            onClick={() => remove.mutate(t.id)}
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
                          onClick={() => setConfirmId(t.id)}
                          aria-label="Delete task"
                          className="shrink-0 rounded-md p-1.5 text-muted transition-colors hover:bg-danger/10 hover:text-danger"
                        >
                          <Trash2 size={15} strokeWidth={1.75} />
                        </button>
                      ),
                  };
                })}
              />
            </>
          ) : (
            <EmptyState
              title="No tasks yet"
              hint="Add one above — then click it to add a body, tags, a project and a due date."
            />
          )}
        </div>
      </div>

      {selected && (
        <>
          {/* Below lg the editor overlays as a right drawer; from lg it's a column. */}
          <div
            aria-hidden
            onClick={() => setSelectedId(null)}
            className="fixed inset-0 z-20 bg-text/25 lg:hidden"
          />
          <aside className="fixed inset-y-0 right-0 z-30 flex w-[min(460px,92vw)] shrink-0 flex-col border-l border-border bg-surface shadow-xl lg:static lg:z-auto lg:w-[420px] lg:shadow-none">
            <TaskEditor
              key={selected.id}
              task={selected}
              onClose={() => setSelectedId(null)}
              onChanged={invalidate}
            />
          </aside>
        </>
      )}
    </div>
  );
}

/** Side-panel editor: a task's note-like fields (Markdown body, tags, project)
 *  plus status + due-date. Local state seeds from the task prop (the list already
 *  carries the full task), so no init effect — the parent remounts via key when
 *  the selection changes. */
function TaskEditor({
  task,
  onClose,
  onChanged,
}: {
  task: Task;
  onClose: () => void;
  onChanged: () => void;
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const projectsQ = useQuery({ queryKey: ["projects"], queryFn: () => api.projects() });
  const tagsQ = useQuery({ queryKey: ["tags"], queryFn: () => api.tags() });

  const [title, setTitle] = useState(task.title);
  const [body, setBody] = useState(task.body ?? "");
  const [status, setStatus] = useState(task.status);
  const [dueDate, setDueDate] = useState((task.due_date ?? "").slice(0, 10));
  const [projectId, setProjectId] = useState(task.project_id ?? "");
  const [tagNames, setTagNames] = useState<string[]>((task.tags ?? []).map((t) => t.name));
  const [preview, setPreview] = useState(false);

  const save = useMutation({
    mutationFn: async () => {
      const tagIds = await api.resolveTagIds(tagNames, tagsQ.data?.tags ?? []);
      qc.invalidateQueries({ queryKey: ["tags"] });
      return api.updateTask(task.id, {
        title: title.trim() || "Untitled",
        body,
        status,
        due_date: dueDate,
        project_id: projectId,
        tag_ids: tagIds,
      });
    },
    onSuccess: onChanged,
    onError: (e) => toast.error(`Couldn't save the task: ${(e as Error).message}`),
  });

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center justify-between border-b border-border px-4 py-3">
        <span className="text-[11.5px] font-medium uppercase tracking-[0.09em] text-faint">
          Edit task
        </span>
        <button
          onClick={onClose}
          aria-label="Close"
          className="rounded-md p-1 text-muted transition-colors hover:bg-surface2 hover:text-text"
        >
          <X size={15} strokeWidth={1.75} />
        </button>
      </div>

      <div className="flex-1 overflow-y-auto px-4 py-4">
        <input
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="Title"
          className="mb-3 w-full bg-transparent font-display text-[18px] font-semibold tracking-tight outline-none placeholder:text-faint"
        />

        <div className="mb-4 flex flex-wrap items-center gap-x-4 gap-y-2 text-[13px]">
          <button
            type="button"
            onClick={() => setStatus(status === "done" ? "open" : "done")}
            className="inline-flex items-center gap-2 text-text"
          >
            <span
              className={`grid size-[18px] place-items-center rounded-md border transition-colors ${
                status === "done"
                  ? "border-accent bg-accent text-white"
                  : "border-border text-transparent"
              }`}
            >
              <Check size={12} strokeWidth={2.5} />
            </span>
            {status === "done" ? "Done" : "Open"}
          </button>
          <label className="inline-flex items-center gap-2 text-muted">
            <CalendarClock size={14} strokeWidth={1.75} />
            <input
              type="date"
              value={dueDate}
              onChange={(e) => setDueDate(e.target.value)}
              className="rounded-lg border border-border bg-surface px-2 py-1 text-[13px] text-text outline-none focus:border-accent"
            />
          </label>
        </div>

        <div className="mb-1 flex items-center justify-between">
          <span className="text-[12.5px] text-muted">Notes</span>
          <button
            type="button"
            onClick={() => setPreview((v) => !v)}
            className="text-[12px] text-muted transition-colors hover:text-accent"
          >
            {preview ? "Write" : "Preview"}
          </button>
        </div>
        {preview ? (
          <div className="prose-answer min-h-[120px] rounded-lg border border-border bg-surface px-3 py-2 text-[14px]">
            <ReactMarkdown remarkPlugins={[remarkGfm]}>{body || "_Nothing yet_"}</ReactMarkdown>
          </div>
        ) : (
          <textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            rows={8}
            placeholder="Add details in Markdown…"
            className="w-full resize-y rounded-lg border border-border bg-surface px-3 py-2 text-[14px] outline-none focus:border-accent placeholder:text-faint"
          />
        )}

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
      </div>

      <div className="flex items-center justify-end gap-3 border-t border-border px-4 py-3">
        {save.error && (
          <span className="mr-auto text-[12px] text-danger">Couldn't save</span>
        )}
        <Button onClick={() => save.mutate()} disabled={save.isPending}>
          {save.isPending ? "Saving…" : "Save"}
        </Button>
      </div>
    </div>
  );
}
