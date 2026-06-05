import { useEffect, useRef, useState } from "react";
import { Mic } from "lucide-react";
import { ASK_PROMPTS } from "../lib/content";
import { usePrefersReducedMotion } from "../lib/useMotion";

/** The hero's "Ask Hygur" field, brought to life. It types out the app's real
 *  example questions one after another — the single signature interaction for a
 *  conversational product. Reduced-motion users get the first prompt, static. */
export function AskTypewriter() {
  const reduced = usePrefersReducedMotion();
  const [text, setText] = useState(reduced ? ASK_PROMPTS[0] : "");
  const promptIdx = useRef(0);
  const charIdx = useRef(0);
  const phase = useRef<"typing" | "holding" | "deleting">("typing");

  useEffect(() => {
    if (reduced) {
      setText(ASK_PROMPTS[0]);
      return;
    }

    let timer: number;
    const tick = () => {
      const full = ASK_PROMPTS[promptIdx.current];
      let delay = 55;

      if (phase.current === "typing") {
        charIdx.current += 1;
        setText(full.slice(0, charIdx.current));
        if (charIdx.current >= full.length) {
          phase.current = "holding";
          delay = 1900;
        }
      } else if (phase.current === "holding") {
        phase.current = "deleting";
        delay = 40;
      } else {
        charIdx.current -= 1;
        setText(full.slice(0, Math.max(charIdx.current, 0)));
        delay = 28;
        if (charIdx.current <= 0) {
          phase.current = "typing";
          promptIdx.current = (promptIdx.current + 1) % ASK_PROMPTS.length;
          delay = 320;
        }
      }
      timer = window.setTimeout(tick, delay);
    };

    timer = window.setTimeout(tick, 700);
    return () => window.clearTimeout(timer);
  }, [reduced]);

  return (
    <div
      className="flex items-center gap-3 rounded-2xl border border-border bg-surface px-4 py-3 shadow-[var(--shadow-soft)]"
      aria-hidden="true"
    >
      <span className="text-sm font-medium text-faint">Ask</span>
      <span className="min-w-0 flex-1 truncate text-[0.95rem] text-text">
        {text}
        {!reduced && <span className="caret h-[1.05em] align-[-0.15em]" />}
      </span>
      <span className="grid h-8 w-8 shrink-0 place-items-center rounded-full bg-accent-weak text-accent">
        <Mic size={15} strokeWidth={1.75} />
      </span>
    </div>
  );
}
