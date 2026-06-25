import { useState } from "react";
import { Share, MoreVertical, Plus, X } from "lucide-react";
import { mobileOS, isStandalone } from "../lib/platform";
import { native } from "../lib/native";

const DISMISS_KEY = "hygur.installPrompt.dismissed";

/** Mobile-web nudge to add Hygur to the home screen — the installable PWA is the
 *  mobile app, so this is how a phone "installs" it. Shows only in a mobile
 *  browser that isn't already standalone (and never in the native shell);
 *  dismissable and remembered. Platform-specific because the gesture differs. */
export function InstallPrompt() {
  const os = mobileOS();
  const [dismissed, setDismissed] = useState(
    () => typeof localStorage !== "undefined" && localStorage.getItem(DISMISS_KEY) === "1",
  );

  if (native.available || !os || isStandalone() || dismissed) return null;

  const dismiss = () => {
    try {
      localStorage.setItem(DISMISS_KEY, "1");
    } catch {
      /* private mode — fine, it just reappears next session */
    }
    setDismissed(true);
  };

  const steps =
    os === "ios" ? (
      <>
        Tap <Share size={14} strokeWidth={2} className="inline align-text-bottom" /> then{" "}
        <span className="font-medium text-text">Add to Home Screen</span>
      </>
    ) : (
      <>
        Tap <MoreVertical size={14} strokeWidth={2} className="inline align-text-bottom" /> then{" "}
        <span className="font-medium text-text">Install app</span>
      </>
    );

  return (
    <div className="fixed inset-x-3 bottom-3 z-[70] mx-auto flex max-w-md items-center gap-3 rounded-2xl border border-border bg-surface px-4 py-3 shadow-lg print:hidden">
      <span className="grid size-9 shrink-0 place-items-center rounded-xl bg-accent-weak text-accent">
        <Plus size={18} strokeWidth={2} />
      </span>
      <div className="min-w-0 flex-1 text-[12.5px] leading-snug text-muted">
        <p className="font-medium text-text">Install Hygur on your phone</p>
        <p className="mt-0.5">{steps}</p>
      </div>
      <button
        onClick={dismiss}
        aria-label="Dismiss"
        className="shrink-0 rounded-md p-1 text-faint transition-colors hover:text-text"
      >
        <X size={16} strokeWidth={2} />
      </button>
    </div>
  );
}
