import { isRemote } from "../lib/connection";
import { passkeysSupported } from "../lib/passkey";
import { useAddPasskey, usePasskeyCount } from "../lib/usePasskey";

const BANNER_TEXT = {
  global:
    "Right now you can only get back into your space from this browser. Add a passkey to sign in from any device.",
  settings:
    "No passkey yet — you can only sign back in from this browser. Add one to keep access from anywhere.",
} as const;

/** Persistent nudge shown to a cloud user with no passkey. `global` is a top strip
 *  across the whole app; `settings` is a card at the top of Settings. Renders
 *  nothing once a passkey exists, on unsupported browsers, or off the cloud shell. */
export function PasskeyBanner({ variant }: { variant: "global" | "settings" }) {
  const { data: count } = usePasskeyCount();
  const { add, busy, ready } = useAddPasskey();
  if (!isRemote() || !passkeysSupported() || count === undefined || count > 0) return null;

  const tone =
    variant === "global"
      ? "border-amber-500/40 bg-amber-500/10 text-amber-800"
      : "border-danger/40 bg-danger/10 text-danger";
  const shape = variant === "global" ? "border-b" : "mb-6 rounded-xl border";

  return (
    <div
      className={`flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-2.5 text-[13px] print:hidden ${shape} ${tone}`}
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
        {busy ? "Adding…" : "Add a passkey"}
      </button>
    </div>
  );
}
