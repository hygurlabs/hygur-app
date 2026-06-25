import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RefreshCw, Plus, Check, Settings2, Trash2 } from "lucide-react";
import { api } from "../lib/api";
import { fmtDateTime } from "../lib/format";
import type { ConnectorHealth, ConnectorInstance, MarketplaceItem } from "../lib/types";
import { ConnectorConfigForm } from "./ConnectorConfigForm";
import { EdgeProtonCard } from "./EdgeProtonCard";
import { EdgeFilesCard } from "./EdgeFilesCard";
import { isDesktop } from "../lib/desktop";
import {
  Badge,
  Button,
  EmptyState,
  ErrorBanner,
  Page,
  PageHeader,
  Skeleton,
} from "../components/ui";

function healthColor(h: ConnectorHealth): string {
  const s = (h.status ?? "").toLowerCase();
  if (["ok", "healthy", "connected", "up"].includes(s)) return "var(--accent)";
  if (["down", "error", "failed", "unhealthy"].includes(s)) return "var(--danger)";
  return "var(--faint)";
}

export function Connectors() {
  const qc = useQueryClient();
  const [configId, setConfigId] = useState<string | null>(null);
  // Two-step delete confirm (window.confirm() is suppressed inside the native
  // WKWebView shell, so we arm the trash button in-UI instead).
  const [armedRemove, setArmedRemove] = useState<string | null>(null);

  // Instances are the source of truth for the "Configured" list: each one has a
  // unique instance_id (singletons appear with instance_id === type_id), so the
  // gear/sync/delete controls address the right connector even with multiple
  // accounts of the same type.
  const instancesQ = useQuery({
    queryKey: ["connector-instances"],
    queryFn: () => api.connectorInstances(),
    refetchInterval: 20_000,
  });
  const marketQ = useQuery({
    queryKey: ["marketplace"],
    queryFn: () => api.marketplace(),
  });
  // Cloud desktop thin client (/edge/status 200): local-source connectors (Proton,
  // Files) run on-device via the edge cards above — hide their pod entries here so
  // there's one place to manage each. (They stay "installed" so the marketplace
  // doesn't re-offer them.)
  const edgeQ = useQuery({ queryKey: ["edge-status"], queryFn: () => api.edgeStatus(), retry: false });
  const thinClient = !edgeQ.isError && !!edgeQ.data;

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["connector-instances"] });
    qc.invalidateQueries({ queryKey: ["connectors"] });
    qc.invalidateQueries({ queryKey: ["marketplace"] });
  };

  const enable = useMutation({
    mutationFn: (id: string) => api.enableConnector(id),
    onSuccess: invalidate,
  });
  const disable = useMutation({
    mutationFn: (id: string) => api.disableConnector(id),
    onSuccess: invalidate,
  });
  const sync = useMutation({
    mutationFn: (id: string) => api.syncConnector(id),
    onSuccess: invalidate,
  });
  const install = useMutation({
    mutationFn: (typeId: string) => api.installConnector(typeId),
    onSuccess: invalidate,
  });
  // Create another account for a multi-instance type, then open its config form
  // so the user can fill in URL/credentials. Created disabled so the empty
  // config doesn't trip the connector's "required" validation on init.
  const addInstance = useMutation({
    mutationFn: async (type: { typeId: string; name: string; count: number }) => {
      const id = `${type.typeId}-${Date.now().toString(36)}`;
      const display_name = type.count > 0 ? `${type.name} ${type.count + 1}` : type.name;
      await api.createConnectorInstance(type.typeId, { id, display_name, enabled: false });
      return id;
    },
    onSuccess: (id) => {
      invalidate();
      setConfigId(id);
    },
  });
  const remove = useMutation({
    mutationFn: (instanceId: string) => api.deleteConnectorInstance(instanceId),
    onSuccess: () => {
      setArmedRemove(null);
      invalidate();
    },
  });

  const allInstances = instancesQ.data ?? [];
  // Edge cards replace these pod connectors on a cloud thin client.
  const edgeReplaced = new Set(["proton", "files"]);
  const instances = allInstances.filter((i) => !(thinClient && edgeReplaced.has(i.type_id)));
  // Multi-instance types present (each has its singleton entry already). Used to
  // render an "Add account" (+) button and to count existing accounts per type.
  const multiTypes = Array.from(
    instances
      .filter((i) => i.info.multi_instance)
      .reduce((m, i) => {
        const e = m.get(i.type_id) ?? { typeId: i.type_id, name: i.info.name, count: 0 };
        e.count += 1;
        m.set(i.type_id, e);
        return m;
      }, new Map<string, { typeId: string; name: string; count: number }>())
      .values(),
  );

  // Catalog entries not yet installed as any instance.
  const installedTypes = new Set(allInstances.map((i) => i.type_id));
  const available = (marketQ.data ?? []).filter(
    (m) => !m.is_installed && !installedTypes.has(m.id),
  );

  const err =
    enable.error || disable.error || sync.error || install.error || addInstance.error || remove.error;

  if (configId) {
    return <ConnectorConfigForm id={configId} onClose={() => setConfigId(null)} />;
  }

  return (
    <Page>
      <PageHeader
        title="Connectors"
        subtitle="Connect your mail, calendars, files and more — and add new sources from the marketplace."
      />

      {err && <ErrorBanner message={`Action failed: ${(err as Error).message}`} />}

      {/* Cloud desktop: local sources (Proton Bridge, filesystem) run on THIS
          device — the pod can't reach them. Each streams to the central KB via the
          edge runner. Desktop-only: a browser can't reach local mail/files, so
          hide these entirely on the web shell. */}
      {isDesktop() && (
        <>
          <EdgeProtonCard />
          <EdgeFilesCard />
        </>
      )}

      <h2 className="mb-2 text-[11.5px] font-medium uppercase tracking-[0.09em] text-faint">
        Configured
      </h2>
      {instancesQ.isLoading ? (
        <Skeleton rows={3} />
      ) : instancesQ.error ? (
        <ErrorBanner
          message={`Couldn't load connectors: ${(instancesQ.error as Error).message}`}
          onRetry={() => instancesQ.refetch()}
        />
      ) : instances.length === 0 ? (
        <EmptyState
          title="No connectors yet"
          hint="Install one from the marketplace below to start syncing data."
        />
      ) : (
        <ul className="border-t border-border">
          {instances.map((c: ConnectorInstance) => {
            const isDynamic = c.instance_id !== c.type_id; // a "+"-added account
            return (
              <li
                key={c.instance_id}
                className="flex items-center gap-3 border-b border-border px-1 py-3.5"
              >
                <span
                  aria-hidden
                  className="size-2.5 shrink-0 rounded-full"
                  style={{ background: healthColor(c.health) }}
                  title={c.health.status ?? "unknown"}
                />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="truncate font-medium">{c.display_name}</span>
                    {c.info.multi_instance && <Badge>{c.type_id}</Badge>}
                    {!c.enabled && <Badge>off</Badge>}
                  </div>
                  <p className="mt-0.5 line-clamp-1 text-[12.5px] text-muted">
                    {c.health.last_error
                      ? c.health.last_error
                      : [
                          c.info.description,
                          typeof c.health.item_count === "number"
                            ? `${c.health.item_count} items`
                            : "",
                          c.health.last_sync && !c.health.last_sync.startsWith("0001")
                            ? `synced ${fmtDateTime(c.health.last_sync)}`
                            : "",
                        ]
                          .filter(Boolean)
                          .join(" · ")}
                  </p>
                </div>
                <div className="flex shrink-0 items-center gap-1.5">
                  <button
                    onClick={() => setConfigId(c.instance_id)}
                    aria-label="Configure"
                    title="Configure"
                    className="rounded-md p-1.5 text-muted transition-colors hover:bg-surface2 hover:text-text"
                  >
                    <Settings2 size={16} strokeWidth={1.9} />
                  </button>
                  {c.enabled && (
                    <button
                      onClick={() => sync.mutate(c.instance_id)}
                      disabled={sync.isPending}
                      aria-label="Sync now"
                      title="Sync now"
                      className="rounded-md p-1.5 text-muted transition-colors hover:bg-surface2 hover:text-text disabled:opacity-40"
                    >
                      <RefreshCw
                        size={16}
                        strokeWidth={1.9}
                        className={sync.isPending ? "animate-spin" : ""}
                      />
                    </button>
                  )}
                  {isDynamic &&
                    (armedRemove === c.instance_id ? (
                      <button
                        onClick={() => remove.mutate(c.instance_id)}
                        onBlur={() => setArmedRemove(null)}
                        disabled={remove.isPending}
                        autoFocus
                        title="Indexed items are kept — click to confirm"
                        className="rounded-md px-2 py-1 text-[12.5px] font-medium text-danger ring-1 ring-danger/40 transition-colors hover:bg-danger/10 disabled:opacity-40"
                      >
                        Remove?
                      </button>
                    ) : (
                      <button
                        onClick={() => setArmedRemove(c.instance_id)}
                        aria-label="Remove account"
                        title="Remove account"
                        className="rounded-md p-1.5 text-muted transition-colors hover:bg-surface2 hover:text-danger"
                      >
                        <Trash2 size={16} strokeWidth={1.9} />
                      </button>
                    ))}
                  <Button
                    variant="ghost"
                    onClick={() =>
                      c.enabled
                        ? disable.mutate(c.instance_id)
                        : enable.mutate(c.instance_id)
                    }
                  >
                    {c.enabled ? "Disable" : "Enable"}
                  </Button>
                </div>
              </li>
            );
          })}
        </ul>
      )}

      {multiTypes.length > 0 && (
        <div className="mt-3 flex flex-wrap gap-2">
          {multiTypes.map((t) => (
            <Button
              key={t.typeId}
              variant="ghost"
              onClick={() => addInstance.mutate(t)}
              disabled={addInstance.isPending}
            >
              <Plus size={15} strokeWidth={2} /> Add {t.name} account
            </Button>
          ))}
        </div>
      )}

      <h2 className="mb-2 mt-10 text-[11.5px] font-medium uppercase tracking-[0.09em] text-faint">
        Marketplace
      </h2>
      {marketQ.isLoading ? (
        <Skeleton rows={3} />
      ) : available.length === 0 ? (
        <EmptyState
          title="Everything's installed"
          hint="All available connectors are already configured."
        />
      ) : (
        <ul className="border-t border-border">
          {available.map((m: MarketplaceItem) => (
            <li
              key={m.id}
              className="flex items-center gap-3 border-b border-border px-1 py-3.5"
            >
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="truncate font-medium">{m.display_name}</span>
                  {m.verified && <Badge>verified</Badge>}
                  {m.multi_instance && <Badge>multi-account</Badge>}
                </div>
                {m.description && (
                  <p className="mt-0.5 line-clamp-1 text-[12.5px] text-muted">
                    {m.description}
                  </p>
                )}
              </div>
              <Button
                variant="ghost"
                onClick={() => install.mutate(m.id)}
                disabled={install.isPending || !m.is_built_in}
              >
                {m.is_installed ? (
                  <>
                    <Check size={15} strokeWidth={2} /> Installed
                  </>
                ) : (
                  <>
                    <Plus size={15} strokeWidth={2} /> Install
                  </>
                )}
              </Button>
            </li>
          ))}
        </ul>
      )}
    </Page>
  );
}
