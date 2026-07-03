import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { Trash2, X } from "lucide-react";
import { api } from "../lib/api";
import type { SessionSummary } from "../lib/types";

// Width (px) of the red trash zone revealed by a left swipe.
const REVEAL_WIDTH = 76;
// Fraction of REVEAL_WIDTH a drag must cross to snap open on release.
const OPEN_THRESHOLD = REVEAL_WIDTH / 2;

export function SessionsPanel({
  activeId,
  onPick,
  onClose,
  onDelete,
}: {
  activeId: string;
  onPick: (id: string) => void;
  onClose: () => void;
  onDelete?: (id: string) => void;
}) {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ["sessions"],
    queryFn: () => api.sessions(),
  });
  const sessions: SessionSummary[] = data?.sessions ?? [];

  // id of the row currently swiped open (revealing the trash zone), if any.
  const [openId, setOpenId] = useState<string | null>(null);
  // Live drag tracking for the row currently being touched. A ref avoids a
  // re-render per pixel; `dragTick` forces the occasional re-render needed
  // to reflect the transform while dragging.
  const dragRef = useRef<{ id: string; startX: number; dx: number } | null>(null);
  const [, setDragTick] = useState(0);

  async function remove(id: string, e: React.SyntheticEvent) {
    e.stopPropagation();
    try {
      await api.deleteSession(id);
      setOpenId((cur) => (cur === id ? null : cur));
      qc.invalidateQueries({ queryKey: ["sessions"] });
      onDelete?.(id);
    } catch {
      /* ignore */
    }
  }

  function offsetFor(id: string): number {
    if (dragRef.current?.id === id) return dragRef.current.dx;
    return openId === id ? -REVEAL_WIDTH : 0;
  }

  function handleTouchStart(id: string) {
    return (e: React.TouchEvent) => {
      dragRef.current = {
        id,
        startX: e.touches[0].clientX,
        dx: openId === id ? -REVEAL_WIDTH : 0,
      };
    };
  }

  function handleTouchMove(id: string) {
    return (e: React.TouchEvent) => {
      const drag = dragRef.current;
      if (!drag || drag.id !== id) return;
      const base = openId === id ? -REVEAL_WIDTH : 0;
      const delta = e.touches[0].clientX - drag.startX;
      drag.dx = Math.min(0, Math.max(-REVEAL_WIDTH, base + delta));
      setDragTick((t) => t + 1);
    };
  }

  function handleTouchEnd(id: string) {
    return () => {
      const drag = dragRef.current;
      dragRef.current = null;
      if (!drag || drag.id !== id) return;
      setOpenId(drag.dx <= -OPEN_THRESHOLD ? id : null);
      setDragTick((t) => t + 1);
    };
  }

  // A tap anywhere outside the open row's trash zone just closes the swipe
  // reveal — it does not also trigger the row underneath. A second, separate
  // tap is required to act on that row. This keeps the swipe-reveal + the
  // deliberate tap on the trash zone as the only way to delete.
  function handleListClickCapture(e: React.MouseEvent) {
    if (!openId) return;
    const target = e.target as HTMLElement;
    if (target.closest(`[data-trash-id="${openId}"]`)) return;
    e.preventDefault();
    e.stopPropagation();
    setOpenId(null);
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
          <ul className="flex flex-col gap-0.5" onClickCapture={handleListClickCapture}>
            {sessions.map((s) => {
              const dragging = dragRef.current?.id === s.id;
              return (
                // Row + trailing action: the whole row is one button; the delete
                // sits beside it (a real sibling button, not a nested one) and is
                // revealed on hover/focus. On touch, swiping the row left reveals
                // a red trash zone underneath instead.
                <li key={s.id} className="group relative overflow-hidden rounded-lg">
                  <div
                    data-trash-id={s.id}
                    className="absolute inset-y-0 right-0 flex w-[76px] items-center justify-center bg-danger"
                  >
                    <button
                      type="button"
                      onClick={(e) => void remove(s.id, e)}
                      aria-label="Delete conversation"
                      className="flex h-full w-full items-center justify-center text-white"
                    >
                      <Trash2 size={17} strokeWidth={1.9} />
                    </button>
                  </div>
                  <div
                    className={openId === s.id || dragging ? "relative bg-surface" : "relative"}
                    style={{
                      transform: `translateX(${offsetFor(s.id)}px)`,
                      transition: dragging ? "none" : "transform 150ms ease-out",
                    }}
                    onTouchStart={handleTouchStart(s.id)}
                    onTouchMove={handleTouchMove(s.id)}
                    onTouchEnd={handleTouchEnd(s.id)}
                    onTouchCancel={handleTouchEnd(s.id)}
                  >
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
                  </div>
                  <button
                    type="button"
                    onClick={(e) => void remove(s.id, e)}
                    aria-label="Delete conversation"
                    className="absolute right-1.5 top-1.5 rounded p-0.5 text-faint opacity-0 transition-opacity hover:text-danger focus:opacity-100 group-hover:opacity-100"
                  >
                    <X size={13} strokeWidth={2} />
                  </button>
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </div>
  );
}
