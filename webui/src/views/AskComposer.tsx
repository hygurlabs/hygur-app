import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowUp,
  AtSign,
  FileText,
  FolderKanban,
  Mail,
  Mic,
  Paperclip,
  Square,
  StickyNote,
  Tag as TagIcon,
  X,
} from "lucide-react";
import { api } from "../lib/api";
import { native } from "../lib/native";
import type { AttachmentRef, Mention } from "../lib/types";
import { ErrorBanner } from "../components/ui";

export interface ProjectFocus {
  id: string;
  name: string;
}

// MARK: - File → attachment (shared by 📎 and drag-and-drop)

// Reads a file as raw (un-prefixed) base64 for inline image/audio attachments.
function fileToBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const res = reader.result as string;
      const comma = res.indexOf(",");
      resolve(comma >= 0 ? res.slice(comma + 1) : res);
    };
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(file);
  });
}

// Encodes a mono AudioBuffer as a 16-bit PCM WAV file, returning un-prefixed base64.
function wavBase64FromBuffer(buf: AudioBuffer): string {
  const ch = buf.getChannelData(0);
  const n = ch.length;
  const view = new DataView(new ArrayBuffer(44 + n * 2));
  const str = (o: number, s: string) => {
    for (let i = 0; i < s.length; i++) view.setUint8(o + i, s.charCodeAt(i));
  };
  str(0, "RIFF");
  view.setUint32(4, 36 + n * 2, true);
  str(8, "WAVE");
  str(12, "fmt ");
  view.setUint32(16, 16, true);
  view.setUint16(20, 1, true); // PCM
  view.setUint16(22, 1, true); // mono
  view.setUint32(24, buf.sampleRate, true);
  view.setUint32(28, buf.sampleRate * 2, true); // byte rate
  view.setUint16(32, 2, true); // block align
  view.setUint16(34, 16, true); // bits/sample
  str(36, "data");
  view.setUint32(40, n * 2, true);
  let o = 44;
  for (let i = 0; i < n; i++) {
    const s = Math.max(-1, Math.min(1, ch[i]));
    view.setInt16(o, s < 0 ? s * 0x8000 : s * 0x7fff, true);
    o += 2;
  }
  const u8 = new Uint8Array(view.buffer);
  let bin = "";
  for (let i = 0; i < u8.length; i++) bin += String.fromCharCode(u8[i]);
  return btoa(bin);
}

// Decodes any browser-playable audio file and re-encodes it as 16 kHz mono
// 16-bit PCM WAV (un-prefixed base64). Used for m4a/AAC, which Gemma's vLLM
// can't decode but WebKit can. Throws a clear error if decoding fails.
async function transcodeToWav16kMono(file: File): Promise<string> {
  const Ctx: typeof AudioContext =
    window.AudioContext ||
    (window as unknown as { webkitAudioContext: typeof AudioContext })
      .webkitAudioContext;
  const Offline: typeof OfflineAudioContext =
    window.OfflineAudioContext ||
    (window as unknown as { webkitOfflineAudioContext: typeof OfflineAudioContext })
      .webkitOfflineAudioContext;
  if (!Ctx || !Offline) {
    throw new Error(`Audio "${file.name}": transcoding not supported by this browser`);
  }
  const buf = await file.arrayBuffer();
  const decodeCtx = new Ctx();
  let decoded: AudioBuffer;
  try {
    decoded = await decodeCtx.decodeAudioData(buf.slice(0));
  } catch {
    throw new Error(`Audio "${file.name}": format not decodable`);
  } finally {
    void decodeCtx.close();
  }
  const rate = 16000;
  const off = new Offline(1, Math.max(1, Math.ceil(decoded.duration * rate)), rate);
  const src = off.createBufferSource();
  src.buffer = decoded;
  src.connect(off.destination);
  src.start();
  return wavBase64FromBuffer(await off.startRendering());
}

// Turns a dropped/picked file into an AttachmentRef: images & audio go inline to
// the multimodal model (F3); everything else is uploaded + indexed as a doc.
// eslint-disable-next-line react-refresh/only-export-components -- shared file→attachment helper, co-located with the composer that owns the picker (HMR-only rule)
export async function buildAttachment(file: File): Promise<AttachmentRef> {
  const mime = file.type || "";
  const ext = (file.name.split(".").pop() || "").toLowerCase();
  if (mime.startsWith("image/")) {
    const data = await fileToBase64(file);
    return { type: "image", data, mime_type: mime, title: file.name };
  }
  if (
    mime.startsWith("audio/") ||
    ["m4a", "mp3", "wav", "ogg", "mp4", "aac", "m4b", "flac", "opus"].includes(ext)
  ) {
    // Gemma's vLLM audio loader can't decode the m4a/AAC container — it silently
    // drops the audio. WebKit *can*, so we transcode AAC (and any unknown
    // container) to 16 kHz mono WAV. ogg/mp3/wav/flac pass through untouched.
    const knownGood = ["ogg", "mp3", "mpeg", "wav", "flac", "opus"];
    const isAac =
      ["m4a", "mp4", "m4b", "aac"].includes(ext) || /mp4|aac|x-m4a/.test(mime);
    if (!isAac && knownGood.includes(ext)) {
      const data = await fileToBase64(file);
      return { type: "audio", data, format: ext === "mpeg" ? "mp3" : ext, title: file.name };
    }
    const data = await transcodeToWav16kMono(file);
    return { type: "audio", data, format: "wav", title: file.name };
  }
  // Documents (PDF/DOCX/text) → index in the KB, attach by reference.
  const res = await api.uploadFile(file);
  return { type: "document", content_id: res.content_id, title: res.title || file.name };
}

// MARK: - Composer (textarea + 📎 + @ + 🎤)

// Matches an in-progress @-mention at the end of the input: an "@" at a word
// boundary (start of line or after whitespace) followed by any run of
// non-whitespace, non-"@" characters. Broader than \w so tag names with ":",
// "-", "/", "." or accents stay searchable; the boundary keeps email addresses
// like "john@gmail.com" from triggering the picker. Group 1 is the query.
const MENTION_RE = /(?:^|\s)@([^\s@]*)$/;

// Strips the trailing "@token" the user typed (keeps any preceding whitespace).
const MENTION_STRIP_RE = /@[^\s@]*$/;

export function AskComposer({
  input,
  setInput,
  onKeyDown,
  onSend,
  onStop,
  streaming,
  taRef,
  attachments,
  setAttachments,
  focusProjects,
  setFocusProjects,
  focusTags,
  setFocusTags,
}: {
  input: string;
  setInput: (v: string) => void;
  onKeyDown: (e: React.KeyboardEvent) => void;
  onSend: () => void;
  onStop: () => void;
  streaming: boolean;
  taRef: React.RefObject<HTMLTextAreaElement | null>;
  attachments: AttachmentRef[];
  setAttachments: React.Dispatch<React.SetStateAction<AttachmentRef[]>>;
  focusProjects: ProjectFocus[];
  setFocusProjects: React.Dispatch<React.SetStateAction<ProjectFocus[]>>;
  focusTags: ProjectFocus[];
  setFocusTags: React.Dispatch<React.SetStateAction<ProjectFocus[]>>;
}) {
  const fileRef = useRef<HTMLInputElement>(null);
  const [uploading, setUploading] = useState(false);
  const [uploadError, setUploadError] = useState<string | null>(null);

  // @-mention state.
  const [mentionQuery, setMentionQuery] = useState<string | null>(null);
  const { data: mentionData, isFetching: mentionsLoading } = useQuery({
    queryKey: ["mentions", mentionQuery],
    queryFn: () => api.mentions(mentionQuery ?? ""),
    enabled: mentionQuery !== null,
  });
  const mentions = mentionData?.mentions ?? [];

  // 🎤 dictation.
  const [recording, setRecording] = useState(false);
  const dictationBase = useRef("");
  useEffect(() => {
    if (!native.dictationAvailable) return;
    return native.dictation.listen((text, isFinal) => {
      const base = dictationBase.current;
      setInput(base ? `${base} ${text}` : text);
      if (isFinal) setRecording(false);
    });
  }, [setInput]);

  function onChange(e: React.ChangeEvent<HTMLTextAreaElement>) {
    const v = e.target.value;
    setInput(v);
    // Capture everything after "@" up to the next whitespace or "@" — not just
    // \w — so queries with ":", "-", "/", "." or accents (common in tag names
    // like "mail:facture" or "RH-contact") keep the picker open and searchable.
    const m = MENTION_RE.exec(v);
    setMentionQuery(m ? m[1] : null);
  }

  function handleKeyDown(e: React.KeyboardEvent) {
    if (e.key === "Escape" && mentionQuery !== null) {
      setMentionQuery(null);
      return;
    }
    onKeyDown(e);
  }

  // Opens the context picker without needing to type "@" — the button users
  // reach for. Shows recent notes/mails/docs/projects/tags to pick from.
  function toggleMentionPicker() {
    setMentionQuery((prev) => (prev === null ? "" : null));
    taRef.current?.focus();
  }

  function pickMention(m: Mention) {
    // Drop the trailing "@token" the user typed.
    setInput(input.replace(MENTION_STRIP_RE, ""));
    setMentionQuery(null);
    if (m.type === "project") {
      setFocusProjects((prev) =>
        prev.some((p) => p.id === m.id) ? prev : [...prev, { id: m.id, name: m.title }],
      );
    } else if (m.type === "tag") {
      setFocusTags((prev) =>
        prev.some((t) => t.id === m.id) ? prev : [...prev, { id: m.id, name: m.title }],
      );
    } else {
      setAttachments((prev) =>
        prev.some((a) => a.type === "document" && a.content_id === m.id)
          ? prev
          : [...prev, { type: "document", content_id: m.id, title: m.title }],
      );
    }
    taRef.current?.focus();
  }

  async function onFiles(files: FileList | null) {
    if (!files || files.length === 0) return;
    setUploadError(null);
    setUploading(true);
    try {
      for (const file of Array.from(files)) {
        const att = await buildAttachment(file);
        setAttachments((prev) => [...prev, att]);
      }
    } catch (e) {
      setUploadError((e as Error).message);
    } finally {
      setUploading(false);
      if (fileRef.current) fileRef.current.value = "";
    }
  }

  async function toggleMic() {
    if (recording) {
      await native.dictation.stop();
      setRecording(false);
    } else {
      dictationBase.current = input;
      const ok = await native.dictation.start();
      if (ok) setRecording(true);
    }
  }

  const hasChips =
    attachments.length > 0 || focusProjects.length > 0 || focusTags.length > 0;

  return (
    <div className="border-t border-border bg-bg/85 px-4 pt-4 pb-[max(1rem,env(safe-area-inset-bottom))] backdrop-blur sm:px-7">
      <div className="relative mx-auto max-w-[760px]">
        {mentionQuery !== null && (
          <ul className="absolute bottom-full z-30 mb-2 max-h-64 w-full overflow-auto rounded-xl border border-border bg-surface py-1 shadow-lg">
            {mentions.length > 0 ? (
              mentions.map((m) => (
                <li key={`${m.type}-${m.id}`}>
                  <button
                    onClick={() => pickMention(m)}
                    className="flex w-full items-center gap-2.5 px-3.5 py-2 text-left text-[13.5px] transition-colors hover:bg-accent-weak/60"
                  >
                    <MentionIcon type={m.type} />
                    <span className="truncate">{m.title}</span>
                    <span className="ml-auto text-[11px] uppercase tracking-wide text-faint">
                      {m.type}
                    </span>
                  </button>
                </li>
              ))
            ) : (
              <li className="px-3.5 py-2 text-[13px] text-muted">
                {mentionsLoading
                  ? "Searching…"
                  : "No matches — type to find notes, mails, docs & projects"}
              </li>
            )}
          </ul>
        )}

        {hasChips && (
          <div className="mb-2 flex flex-wrap gap-1.5">
            {focusProjects.map((p) => (
              <Chip
                key={`p-${p.id}`}
                icon={<FolderKanban size={12} strokeWidth={2} />}
                label={p.name}
                onRemove={() =>
                  setFocusProjects((prev) => prev.filter((x) => x.id !== p.id))
                }
              />
            ))}
            {focusTags.map((t) => (
              <Chip
                key={`t-${t.id}`}
                icon={<TagIcon size={12} strokeWidth={2} />}
                label={t.name}
                onRemove={() =>
                  setFocusTags((prev) => prev.filter((x) => x.id !== t.id))
                }
              />
            ))}
            {attachments.map((a, i) => (
              <Chip
                key={`a-${i}-${a.title ?? (a.type === "document" ? a.content_id : a.type)}`}
                icon={<Paperclip size={12} strokeWidth={2} />}
                label={a.title ?? (a.type === "document" ? a.content_id : a.type)}
                onRemove={() =>
                  setAttachments((prev) => prev.filter((_, j) => j !== i))
                }
              />
            ))}
          </div>
        )}

        {uploadError && (
          <div className="mb-2">
            <ErrorBanner message={`Upload failed: ${uploadError}`} />
          </div>
        )}

        <div className="flex items-end gap-1.5 rounded-2xl border border-border bg-surface py-2 pl-2 pr-2 focus-within:border-accent">
          <input
            ref={fileRef}
            type="file"
            multiple
            className="hidden"
            onChange={(e) => void onFiles(e.target.files)}
          />
          <ComposerIcon
            label="Attach a document"
            onClick={() => fileRef.current?.click()}
            disabled={uploading}
            spinning={uploading}
          >
            <Paperclip size={17} strokeWidth={1.9} />
          </ComposerIcon>
          <ComposerIcon
            label="Add context (notes, mails, projects, tags)"
            onClick={toggleMentionPicker}
            active={mentionQuery !== null}
          >
            <AtSign size={17} strokeWidth={1.9} />
          </ComposerIcon>

          <textarea
            ref={taRef}
            rows={1}
            value={input}
            onChange={onChange}
            onKeyDown={handleKeyDown}
            placeholder="Ask anything — @ to add context, 📎 to attach…"
            className="max-h-[168px] flex-1 resize-none bg-transparent py-1.5 text-[14.5px] text-text outline-none placeholder:text-faint"
          />

          {native.dictationAvailable && (
            <ComposerIcon
              label={recording ? "Stop dictation" : "Dictate"}
              onClick={() => void toggleMic()}
              active={recording}
            >
              <Mic size={17} strokeWidth={1.9} />
            </ComposerIcon>
          )}

          {streaming ? (
            <button
              onClick={onStop}
              aria-label="Stop"
              title="Stop generating"
              className="grid size-9 shrink-0 place-items-center rounded-xl bg-accent text-white transition-opacity hover:opacity-90"
            >
              <Square size={15} strokeWidth={2.2} className="fill-current" />
            </button>
          ) : (
            <button
              onClick={onSend}
              disabled={!input.trim()}
              aria-label="Send"
              className="grid size-9 shrink-0 place-items-center rounded-xl bg-accent text-white transition-opacity hover:opacity-90 disabled:opacity-30"
            >
              <ArrowUp size={18} strokeWidth={2.2} />
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

function ComposerIcon({
  children,
  label,
  onClick,
  disabled,
  active,
  spinning,
}: {
  children: React.ReactNode;
  label: string;
  onClick: () => void;
  disabled?: boolean;
  active?: boolean;
  spinning?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      aria-label={label}
      title={label}
      className={`grid size-9 shrink-0 place-items-center rounded-xl transition-colors disabled:opacity-40 ${
        active
          ? "bg-accent text-white"
          : "text-muted hover:bg-surface2 hover:text-text"
      } ${spinning ? "animate-pulse" : ""}`}
    >
      {children}
    </button>
  );
}

function Chip({
  icon,
  label,
  onRemove,
}: {
  icon: React.ReactNode;
  label: string;
  onRemove: () => void;
}) {
  return (
    <span className="inline-flex max-w-[220px] items-center gap-1.5 rounded-full border border-border bg-surface py-1 pl-2.5 pr-1.5 text-[12.5px]">
      <span className="text-accent">{icon}</span>
      <span className="truncate">{label}</span>
      <button
        onClick={onRemove}
        aria-label="Remove"
        className="rounded-full p-0.5 text-faint transition-colors hover:bg-surface2 hover:text-danger"
      >
        <X size={12} strokeWidth={2} />
      </button>
    </span>
  );
}

function MentionIcon({ type }: { type: Mention["type"] }) {
  const cls = "shrink-0 text-faint";
  if (type === "project")
    return <FolderKanban size={15} strokeWidth={1.75} className={cls} />;
  if (type === "tag") return <TagIcon size={15} strokeWidth={1.75} className={cls} />;
  if (type === "note")
    return <StickyNote size={15} strokeWidth={1.75} className={cls} />;
  if (type === "mail") return <Mail size={15} strokeWidth={1.75} className={cls} />;
  return <FileText size={15} strokeWidth={1.75} className={cls} />;
}
