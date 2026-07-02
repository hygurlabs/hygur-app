import { useState } from "react";

// Type-to-confirm deletion gate (AWS-style): the "Cancel & delete" link to the
// Stripe portal only activates once the user types their exact space name.
export function DeleteSpaceRow({ portalURL, instanceName }: { portalURL: string; instanceName: string }) {
  const [confirm, setConfirm] = useState("");
  const expected = instanceName || "DELETE";
  const matched = confirm.trim() === expected;
  // Cancelling via the Stripe billing portal IS the deletion (per the Terms:
  // access runs to the end of the paid period, then crypto-shred + purge). There
  // is no email path — when the portal isn't configured yet, the action is simply
  // not yet available.
  const canCancel = portalURL !== "";
  return (
    <div className="px-4 py-3">
      <p className="text-[14px]">Delete my space</p>
      <p className="mt-0.5 text-[12.5px] text-muted">
        Cancelling ends your subscription. Your access continues until the end of the paid period
        (no refund). When it ends, your encryption key is destroyed immediately and the space is
        permanently purged after 30 days. This cannot be undone.
      </p>
      <p className="mt-2 text-[12.5px] text-muted">
        Want a copy? Export your data before you cancel.
      </p>
      {instanceName ? (
        <p className="mt-2 text-[12.5px] text-muted">
          To confirm, type your space name{" "}
          <code className="select-all rounded bg-surface2 px-1.5 py-0.5 font-mono text-[12px] text-text">
            {instanceName}
          </code>{" "}
          below.
        </p>
      ) : (
        <p className="mt-2 text-[12.5px] text-muted">
          To confirm, type{" "}
          <code className="select-all rounded bg-surface2 px-1.5 py-0.5 font-mono text-[12px] text-text">
            DELETE
          </code>{" "}
          below.
        </p>
      )}
      <div className="mt-2 flex flex-col gap-2 sm:flex-row sm:items-center">
        <input
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          placeholder={`Type "${expected}" to confirm`}
          spellCheck={false}
          autoCapitalize="off"
          className="flex-1 rounded-lg border border-border bg-surface px-3 py-1.5 text-[13px] outline-none focus:border-danger"
        />
        {!canCancel ? (
          <span className="shrink-0 cursor-not-allowed rounded-lg border border-border px-3 py-1.5 text-center text-[13px] font-medium text-faint">
            Available at launch
          </span>
        ) : matched ? (
          <a
            href={portalURL}
            target="_blank"
            rel="noopener noreferrer"
            className="shrink-0 rounded-lg border border-danger/50 bg-danger/10 px-3 py-1.5 text-center text-[13px] font-medium text-danger hover:bg-danger/20"
          >
            Cancel &amp; delete
          </a>
        ) : (
          <span className="shrink-0 cursor-not-allowed rounded-lg border border-border px-3 py-1.5 text-center text-[13px] font-medium text-faint">
            Cancel &amp; delete
          </span>
        )}
      </div>
    </div>
  );
}
