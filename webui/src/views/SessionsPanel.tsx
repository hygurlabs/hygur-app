import { useQuery, useQueryClient } from "@tanstack/react-query";
import { X } from "lucide-react";
import { api } from "../lib/api";
import type { SessionSummary } from "../lib/types";

export function SessionsPanel({
  activeId,
  onPick,
  onClose,
}: {
  activeId: string;
  onPick: (id: string) => void;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ["sessions"],
    queryFn: () => api.sessions(),
  });
  const sessions: SessionSummary[] = data?.sessions ?? [];

  async function remove(id: string, e: React.MouseEvent) {
    e.stopPropagation();
    try {
      await api.deleteSession(id);
      qc.invalidateQueries({ queryKey: ["sessions"] });
    } catch {
      /* ignore */
    }
  }

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center justify-between border-b border-border px-4 py-3">
        <span className="text-[11.5px] font-medium uppercase tracking-[0.09em] text-faint">
          Conversations
        </span>
        <button
          onClick={onClose}
          aria-label="Close"
          className="rounded-md p-1 text-muted transition-colors hover:bg-surface2 hover:text-text"
        >
          <X size={15} strokeWidth={1.75} />
        </button>
      </div>
      <div className="flex-1 overflow-y-auto p-2">
        {isLoading ? (
          <p className="px-2 py-3 text-[13px] text-muted">Loading…</p>
        ) : sessions.length === 0 ? (
          <p className="px-2 py-3 text-[13px] text-muted">No past conversations yet.</p>
        ) : (
          <ul className="flex flex-col gap-0.5">
            {sessions.map((s) => (
              // Row + trailing action: the whole row is one button; the delete
              // sits beside it (a real sibling button, not a nested one) and is
              // revealed on hover/focus.
              <li key={s.id} className="group relative">
                <button
                  onClick={() => onPick(s.id)}
                  className={`flex w-full flex-col items-start gap-0.5 rounded-lg py-2 pl-2.5 pr-9 text-left transition-colors ${
                    s.id === activeId ? "bg-accent-weak" : "hover:bg-surface2"
                  }`}
                >
                  <span className="w-full truncate text-[13.5px] font-medium">
                    {s.title || "Untitled"}
                  </span>
                  {s.last_message && (
                    <span className="line-clamp-1 text-[12px] text-muted">
                      {s.last_message}
                    </span>
                  )}
                </button>
                <button
                  type="button"
                  onClick={(e) => void remove(s.id, e)}
                  aria-label="Delete conversation"
                  className="absolute right-1.5 top-1.5 rounded p-0.5 text-faint opacity-0 transition-opacity hover:text-danger focus:opacity-100 group-hover:opacity-100"
                >
                  <X size={13} strokeWidth={2} />
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
