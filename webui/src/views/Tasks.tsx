import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Check, Trash2, FolderKanban } from "lucide-react";
import { api } from "../lib/api";
import { fmtDate } from "../lib/format";
import type { Task } from "../lib/types";
import {
  Button,
  EmptyState,
  ErrorBanner,
  Page,
  PageHeader,
  Skeleton,
  TextInput,
} from "../components/ui";

export function Tasks() {
  const qc = useQueryClient();
  const [title, setTitle] = useState("");

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
    onSuccess: () => {
      setTitle("");
      invalidate();
    },
  });
  const toggle = useMutation({
    mutationFn: (t: Task) =>
      api.updateTask(t.id, { status: t.status === "done" ? "open" : "done" }),
    onSuccess: invalidate,
  });
  const remove = useMutation({
    mutationFn: (id: string) => api.deleteTask(id),
    onSuccess: invalidate,
  });

  // Server already orders open-first, newest-first; render as returned.
  const tasks = data?.tasks ?? [];
  const openCount = tasks.filter((t) => t.status !== "done").length;

  return (
    <Page>
      <PageHeader
        title="Tasks"
        subtitle="A simple local to-do list. Capture follow-ups from mail and notes, check them off when done."
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
      {create.error && (
        <ErrorBanner
          message={`Couldn't add the task: ${(create.error as Error).message}`}
        />
      )}

      {isLoading ? (
        <Skeleton rows={4} />
      ) : tasks.length > 0 ? (
        <>
          <div className="mb-2 text-[12.5px] text-muted">
            {openCount} open · {tasks.length - openCount} done
          </div>
          <ul className="divide-y divide-border rounded-xl border border-border bg-surface">
            {tasks.map((t) => {
              const done = t.status === "done";
              const proj = projectName(t.project_id);
              return (
                <li key={t.id} className="flex items-center gap-3 px-3.5 py-2.5">
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
                  <div className="min-w-0 flex-1">
                    <p
                      className={`truncate text-[14px] ${
                        done ? "text-muted line-through" : "text-text"
                      }`}
                    >
                      {t.title}
                    </p>
                    {(proj || t.due_date) && (
                      <div className="mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[12px] text-muted">
                        {proj && (
                          <span className="inline-flex items-center gap-1">
                            <FolderKanban size={12} strokeWidth={1.75} />
                            {proj}
                          </span>
                        )}
                        {t.due_date && <span className="tnum">due {fmtDate(t.due_date)}</span>}
                      </div>
                    )}
                  </div>
                  <button
                    onClick={() => remove.mutate(t.id)}
                    aria-label="Delete task"
                    className="shrink-0 rounded-md p-1.5 text-muted transition-colors hover:bg-danger/10 hover:text-danger"
                  >
                    <Trash2 size={15} strokeWidth={1.75} />
                  </button>
                </li>
              );
            })}
          </ul>
        </>
      ) : (
        <EmptyState
          title="No tasks yet"
          hint="Add one above, or create a task from any mail or note via its detail panel."
        />
      )}
    </Page>
  );
}
