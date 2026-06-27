import {
  ShieldCheck,
  Cpu,
  PlugZap,
  MonitorSmartphone,
  Cloud,
  Blocks,
  Quote,
  Scale,
  Lock,
  Sunrise,
  ListChecks,
  History,
  CalendarClock,
  Network,
  Mail,
  AtSign,
  Inbox,
  FolderOpen,
  Calendar,
  Gem,
  NotebookText,
  type LucideIcon,
} from "lucide-react";

/** App CTAs point to the public repo. */
export const GITHUB_URL = "https://github.com/hygurlabs/hygur-app";

/** Download CTAs ("Get Hygur" / "Download the app") point at the latest GitHub
 *  release, where the signed app builds live. Source/license links stay on the repo. */
export const RELEASES_URL = "https://github.com/hygurlabs/hygur-app/releases/latest";

/** Hygur Cloud subscription — Stripe payment link (Personal, 35 €/mo standard;
 *  launch offer 29.90 €/mo locked for life for the first 2,000 subscribers). */
export const CLOUD_SUBSCRIBE_URL = "https://buy.stripe.com/bJe8wIdJRbZHfxa1ou2Fa05";

/** Master gate for paid signups. Keep FALSE until the production LLM backend is
 *  settled — closed = the Cloud card shows "coming soon" and never opens Stripe
 *  checkout (the payment link is also deactivated server-side). Flip to true +
 *  reactivate the Stripe payment link to open subscriptions. */
export const CLOUD_OPEN = false;

/** Real example prompts from the app's "Ask Hygur" screen, replayed in the
 *  hero so the conversational product speaks for itself. */
export const ASK_PROMPTS = [
  "Where do things stand right now?",
  "What have I decided lately, and why?",
  "What needs my attention this week?",
] as const;

/** Runtimes Hygur can point at — set in mono as small tokens. */
export const RUNTIMES = ["LM Studio", "Ollama", "vLLM", "llama.cpp"] as const;

export interface Principle {
  icon: LucideIcon;
  title: string;
  body: string;
}

/** Lifted from the product's own welcome screen — same voice, no invention. */
export const PRINCIPLES: Principle[] = [
  {
    icon: ShieldCheck,
    title: "Private by design",
    body: "Run it fully local and your data stays on your machine — the app talks only to a local sidecar and the AI runtime you point it at.",
  },
  {
    icon: Cpu,
    title: "Your model, your rules",
    body: "Bring any OpenAI-compatible runtime: LM Studio, Ollama, vLLM or llama.cpp. No vendor lock-in.",
  },
  {
    icon: PlugZap,
    title: "Connect what matters",
    body: "Index mail, calendar and folders so Hygur can answer with your own context, not the public web.",
  },
];

/** What the local-first, grounded approach actually buys you — the trust layer
 *  (cited answers + caught contradictions). Same voice as the product. */
export const GROUNDING: Principle[] = [
  {
    icon: Quote,
    title: "Answers you can check",
    body: "When Hygur answers from your data, it cites the exact mail, notes and documents it used — your sources, not the public web. Dates and amounts are pulled from those documents, not made up.",
  },
  {
    icon: Scale,
    title: "Catches contradictions",
    body: "When two of your own sources disagree on the same fact, Hygur surfaces it, both values quoted and dated, so you decide what still holds.",
  },
  {
    icon: Lock,
    title: "Never leaves your side",
    body: "It runs on your machine, or a private EU instance that's yours alone. No training on your data, ever.",
  },
];

/** The daily payoff of the psyche layer: what Hygur does for you so you spend
 *  less time remembering and chasing. Outcome-first, same plain voice. */
export const DAILY: Principle[] = [
  {
    icon: Sunrise,
    title: "A brief, not an inbox",
    body: "Each morning, what moved and what needs you today, ranked so there's nothing to triage.",
  },
  {
    icon: ListChecks,
    title: "What needs you, first",
    body: "Decisions to make, deadlines and follow-ups rise to the top before they slip.",
  },
  {
    icon: History,
    title: "Decisions, remembered",
    body: "Hygur keeps what you decided and why, and flags it when a later source disagrees.",
  },
  {
    icon: CalendarClock,
    title: "It sees what's coming",
    body: "Recurring bills, renewals and obligations surface before they fall due.",
  },
];

export interface Edition {
  id: string;
  name: string;
  kicker: string;
  tagline: string;
  body: string;
  badges: string[];
  icon: LucideIcon;
  cta: string;
  /** Per-edition CTA link; falls back to GITHUB_URL when unset. */
  href?: string;
  /** Featured editions get the warm "core" treatment and span wider. */
  featured?: boolean;
  /** Whether the CTA goes somewhere. "Soon" cards (no real destination) render
   *  as a static container, not a link, so they don't read as clickable. */
  actionable?: boolean;
}

export const EDITIONS: Edition[] = [
  {
    id: "app",
    name: "Hygur App",
    kicker: "Clients",
    tagline: "Mac app · installable on mobile",
    body: "The local twin on your desk and in your pocket. Ask, search and capture on your Mac, and on your phone as an installable web app. It runs on your machine and talks only to the AI runtime you point it at, local or any OpenAI-compatible endpoint.",
    badges: ["Free", "Open source · AGPL"],
    icon: MonitorSmartphone,
    cta: "Get the app",
    href: RELEASES_URL,
    actionable: true,
  },
  {
    id: "cloud",
    name: "Hygur Cloud",
    kicker: "Managed",
    tagline: "Fully managed · 29.90 €/mo launch, then 35 €/mo",
    body: "A managed Hygur instance, one per account, running exclusively on EU servers. GPU inference runs at an EU provider that never trains on your data. We run and update it; you keep control of your data, and can export or delete it anytime.",
    badges: CLOUD_OPEN
      ? ["Hosted", "29.90 €/mo · launch", "EU-only · no training"]
      : ["29.90 €/mo · launch", "EU-only · no training", "Coming soon"],
    icon: Cloud,
    cta: CLOUD_OPEN ? "Subscribe" : "Coming soon",
    // Closed: no destination (static card). Open: the pre-checkout recap page (#/subscribe),
    // which recaps price + terms before sending to Stripe.
    href: CLOUD_OPEN ? "#/subscribe" : undefined,
    actionable: CLOUD_OPEN,
    featured: true,
  },
  {
    id: "marketplace",
    name: "Hygur Marketplace",
    kicker: "Ecosystem",
    tagline: "Connectors catalogue",
    body: "A growing catalogue of connectors that bring more of your world in. Mail, files and calendars today, all free. Deeper, pro-grade connectors will follow as paid add-ons.",
    badges: ["Free today"],
    icon: Blocks,
    cta: "Browse connectors",
    href: "#/connectors",
    actionable: true,
  },
  {
    id: "teams",
    name: "Hygur Teams",
    kicker: "Soon",
    tagline: "Shared memory for teams & projects",
    body: "Everyone keeps their private twin. A project gets its own shared memory, a mesh of connected minds where the team's mail, notes and decisions live together, with provenance and a daily brief.",
    badges: ["Soon"],
    icon: Network,
    cta: "Coming soon",
    actionable: false,
  },
];

export interface Connector {
  name: string;
  category: string;
  body: string;
  icon: LucideIcon;
  /** Built but not yet shipped — rendered in the "Coming soon" group. */
  soon?: boolean;
}

/** The connector catalogue, mirroring the app's built-in sources. Marketing copy;
 *  the live list is served by the app's marketplace API. */
export const CONNECTORS: Connector[] = [
  {
    name: "Gmail",
    category: "Mail",
    body: "Threads, attachments and labels, synced over OAuth.",
    icon: AtSign,
  },
  {
    name: "Proton Mail",
    category: "Mail",
    body: "Encrypted mail through Proton Bridge, indexed on your own device.",
    icon: ShieldCheck,
  },
  {
    name: "IMAP",
    category: "Mail",
    body: "Any IMAP mailbox: Fastmail, a work account, your own server.",
    icon: Mail,
  },
  {
    name: "Apple Mail",
    category: "Mail",
    body: "Your macOS Mail accounts, read on-device. Nothing leaves the Mac.",
    icon: Inbox,
  },
  {
    name: "Local files",
    category: "Files",
    body: "Watch folders and index PDF, Word, Markdown and plain text.",
    icon: FolderOpen,
  },
  {
    name: "Calendar",
    category: "Calendar",
    body: "Bring events in from any CalDAV or public iCal feed.",
    icon: Calendar,
  },
  {
    name: "Obsidian",
    category: "Notes",
    body: "Your vault, indexed alongside the rest of your world.",
    icon: Gem,
    soon: true,
  },
  {
    name: "Notion",
    category: "Notes",
    body: "Pages and databases, pulled into your memory.",
    icon: NotebookText,
    soon: true,
  },
];
