import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { TagInput } from "./TagInput";

/** Editable tags + project for a knowledge item, shown in the document panel
 *  (Library / Search). Changes persist immediately. */
export function ItemMeta({ contentId }: { contentId: string }) {
  const qc = useQueryClient();
  const itemQ = useQuery({
    queryKey: ["kb-item", contentId],
    queryFn: () => api.knowledgeItem(contentId),
  });
  const tagsQ = useQuery({ queryKey: ["tags"], queryFn: () => api.tags() });
  const projectsQ = useQuery({ queryKey: ["projects"], queryFn: () => api.projects() });

  const item = itemQ.data;
  const currentTags = item?.tags ?? [];
  const currentNames = currentTags.map((t) => t.name);
  const projectId = item?.project_id ?? "";

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["kb-item", contentId] });
    qc.invalidateQueries({ queryKey: ["knowledge-items"] });
  };

  const updateTags = useMutation({
    mutationFn: async (names: string[]) => {
      const lowerNew = names.map((n) => n.toLowerCase());
      const lowerCur = currentNames.map((n) => n.toLowerCase());
      for (const t of currentTags) {
        if (!lowerNew.includes(t.name.toLowerCase())) {
          await api.removeItemTag(contentId, t.id);
        }
      }
      const toAdd = names.filter((n) => !lowerCur.includes(n.toLowerCase()));
      if (toAdd.length) {
        const ids = await api.resolveTagIds(toAdd, tagsQ.data?.tags ?? []);
        for (const id of ids) await api.addItemTag(contentId, id);
      }
    },
    onSuccess: () => {
      invalidate();
      qc.invalidateQueries({ queryKey: ["tags"] });
    },
  });

  const setProject = useMutation({
    mutationFn: async (pid: string) => {
      if (pid) await api.linkItemToProject(contentId, pid);
      else await api.unlinkItemFromProject(contentId);
    },
    onSuccess: invalidate,
  });

  const dismissSuggestion = useMutation({
    mutationFn: () => api.dismissProjectSuggestion(contentId),
    onSuccess: invalidate,
  });

  // Proactive project suggestion (W4): only when the item has no project yet.
  const suggestedId =
    !projectId && typeof item?.metadata?.suggested_project_id === "string"
      ? (item.metadata.suggested_project_id as string)
      : "";
  const suggestedName = suggestedId
    ? (projectsQ.data ?? []).find((p) => p.id === suggestedId)?.name
    : undefined;

  if (itemQ.isLoading || !item) return null;

  return (
    <div className="mb-5 border-b border-border pb-5">
      <div className="mb-3 flex items-center gap-2.5">
        <span className="w-14 shrink-0 text-[12px] text-muted">Projet</span>
        <select
          value={projectId}
          onChange={(e) => setProject.mutate(e.target.value)}
          className="min-w-0 flex-1 rounded-lg border border-border bg-surface px-2.5 py-1.5 text-[13px] outline-none focus:border-accent"
        >
          <option value="">Aucun projet</option>
          {(projectsQ.data ?? []).map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      </div>
      {suggestedName && (
        <div className="mb-3 flex items-center gap-2.5">
          <span className="w-14 shrink-0" />
          <div className="flex min-w-0 flex-1 items-center gap-2">
            <span className="truncate rounded-full bg-accent-weak px-2 py-0.5 text-[12px] text-accent">
              Suggéré : {suggestedName}
            </span>
            <button
              onClick={() => setProject.mutate(suggestedId)}
              className="shrink-0 text-[12px] font-medium text-accent hover:underline"
            >
              Ajouter
            </button>
            <button
              onClick={() => dismissSuggestion.mutate()}
              className="shrink-0 text-[12px] text-muted hover:text-text"
            >
              Écarter
            </button>
          </div>
        </div>
      )}
      <div className="flex items-start gap-2.5">
        <span className="w-14 shrink-0 pt-2 text-[12px] text-muted">Étiquettes</span>
        <div className="min-w-0 flex-1">
          <TagInput
            value={currentNames}
            suggestions={(tagsQ.data?.tags ?? []).map((t) => t.name)}
            onChange={(names) => updateTags.mutate(names)}
          />
        </div>
      </div>
    </div>
  );
}
