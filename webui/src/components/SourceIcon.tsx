import {
  Mail,
  StickyNote,
  Paperclip,
  Newspaper,
  FileText,
  type LucideIcon,
} from "lucide-react";

// Maps a knowledge_items source_type to a glyph so lists (Library, Search,
// chat sources) show at a glance where a result came from.
const MAP: Record<string, LucideIcon> = {
  mail: Mail,
  email: Mail,
  thread: Mail,
  note: StickyNote,
  pdf: Paperclip,
  docx: Paperclip,
  markdown: FileText,
  md: FileText,
  txt: FileText,
  file: Paperclip,
  event: Newspaper,
  brief: Newspaper,
  meeting_brief: Newspaper,
};

export function SourceIcon({ type, size = 15 }: { type?: string; size?: number }) {
  const Icon = (type && MAP[type]) || FileText;
  return <Icon size={size} strokeWidth={1.75} className="shrink-0 text-faint" />;
}
