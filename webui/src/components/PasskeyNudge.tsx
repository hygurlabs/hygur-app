import { isRemote } from "../lib/connection";
import { passkeysSupported } from "../lib/passkey";
import { useAddPasskey, usePasskeyCount } from "../lib/usePasskey";

const BANNER_TEXT = {
  global:
    "No passkey set — without one, you'll lose access to your account and all your data if your subscription ever lapses or is canceled.",
  settings:
    "No passkey yet — you can only sign back in from this browser. Add one to keep access from anywhere.",
} as const;

/** Persistent nudge shown to a cloud user with no passkey. `global` is a red
 *  data-loss warning strip in the top nav (a passkey is the only self-serve
 *  recovery path — no passkey means operator-only recovery if a subscription
 *  lapses); `settings` is a card at the top of Settings. Renders nothing once a
 *  passkey exists, on unsupported browsers, or off the cloud shell. */
export function PasskeyBanner({ variant }: { variant: "global" | "settings" }) {
  const { data: count } = usePasskeyCount();
  const { add, busy, ready } = useAddPasskey();
  if (!isRemote() || !passkeysSupported() || count === undefined || count > 0) return null;

  const shape = variant === "global" ? "border-b" : "mb-6 rounded-xl border";

  return (
    <div
      // Permanent data-loss stakes → announce it (only the global strip carries the
      // role, so the Settings page — which renders both — doesn't double-announce).
      role={variant === "global" ? "alert" : undefined}
      className={`flex flex-wrap items-center gap-x-3 gap-y-2 border-danger/40 bg-danger/10 px-4 py-2.5 text-[13px] text-danger print:hidden ${shape}`}
    >
      <span aria-hidden className="text-[15px] leading-none">
        ⚠
      </span>
      <p className="min-w-0 flex-1">{BANNER_TEXT[variant]}</p>
      <button
        onClick={() => void add()}
        disabled={busy || !ready}
        className="shrink-0 rounded-md bg-accent px-3 py-1 font-medium text-white transition-opacity hover:opacity-90 disabled:opacity-50"
      >
        {busy ? "Adding…" : variant === "global" ? "Set up a passkey" : "Add a passkey"}
      </button>
    </div>
  );
}
