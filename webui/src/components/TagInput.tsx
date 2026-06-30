import { useState } from "react";
import { X } from "lucide-react";

/** A tag editor: current tags as removable chips + a text field that
 *  autocompletes from existing tags (and offers to create a new one). Operates
 *  on tag NAMES; callers map names ↔ ids / free-form arrays as needed. */
export function TagInput({
  value,
  suggestions,
  onChange,
  placeholder,
}: {
  value: string[];
  suggestions: string[];
  onChange: (next: string[]) => void;
  placeholder?: string;
}) {
  const [text, setText] = useState("");
  const lower = text.trim().toLowerCase();

  const has = (name: string) =>
    value.some((v) => v.toLowerCase() === name.toLowerCase());

  const matches = lower
    ? suggestions
        .filter((s) => s.toLowerCase().includes(lower) && !has(s))
        .slice(0, 8)
    : [];
  const exactExists =
    suggestions.some((s) => s.toLowerCase() === lower) || has(lower);

  function add(name: string) {
    const n = name.trim();
    if (!n || has(n)) {
      setText("");
      return;
    }
    onChange([...value, n]);
    setText("");
  }
  function remove(name: string) {
    onChange(value.filter((v) => v !== name));
  }

  return (
    <div className="relative">
      <div className="flex flex-wrap items-center gap-1.5 rounded-lg border border-border bg-surface px-2 py-1.5 focus-within:border-accent">
        {value.map((t) => (
          <span
            key={t}
            className="inline-flex items-center gap-1 rounded-full bg-accent-weak px-2 py-0.5 text-[12px] text-accent"
          >
            {t}
            <button
              onClick={() => remove(t)}
              aria-label={`Retirer ${t}`}
              className="text-accent/70 transition-colors hover:text-danger"
            >
              <X size={11} strokeWidth={2.5} />
            </button>
          </span>
        ))}
        <input
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              add(text);
            } else if (e.key === "Backspace" && !text && value.length) {
              remove(value[value.length - 1]);
            }
          }}
          placeholder={value.length ? "" : (placeholder ?? "Ajouter des étiquettes…")}
          className="min-w-[90px] flex-1 bg-transparent py-0.5 text-[13px] outline-none placeholder:text-faint"
        />
      </div>

      {lower && (matches.length > 0 || !exactExists) && (
        <ul className="absolute z-30 mt-1 max-h-48 w-full overflow-auto rounded-lg border border-border bg-surface py-1 shadow-lg">
          {matches.map((m) => (
            <li key={m}>
              <button
                onClick={() => add(m)}
                className="block w-full px-3 py-1.5 text-left text-[13px] transition-colors hover:bg-accent-weak/60"
              >
                {m}
              </button>
            </li>
          ))}
          {!exactExists && (
            <li>
              <button
                onClick={() => add(text)}
                className="block w-full px-3 py-1.5 text-left text-[13px] text-accent transition-colors hover:bg-accent-weak/60"
              >
                Créer « {text.trim()} »
              </button>
            </li>
          )}
        </ul>
      )}
    </div>
  );
}
