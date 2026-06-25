import {
  ShieldCheck,
  Cpu,
  PlugZap,
  MonitorSmartphone,
  Server,
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
  type LucideIcon,
} from "lucide-react";

/** App / Server CTAs point to the public repo. */
export const GITHUB_URL = "https://github.com/hygurlabs/hygur-app";

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
  "What have I decided lately — and why?",
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
    body: "Your data never leaves your machine. The app talks only to a local sidecar and the AI runtime you point it at.",
  },
  {
    icon: Cpu,
    title: "Your model, your rules",
    body: "Bring any OpenAI-compatible runtime — LM Studio, Ollama, vLLM or llama.cpp. No vendor lock-in.",
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
    body: "Every reply cites your own mail, notes and documents — never the public web, never invented. Dates and amounts are computed, not guessed.",
  },
  {
    icon: Scale,
    title: "Catches contradictions",
    body: "When two of your own sources disagree on the same fact, Hygur surfaces it — both values, quoted and dated — so you decide what still holds.",
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
    body: "Each morning, what moved and what needs you today — ranked, so there's nothing to triage.",
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
}

export const EDITIONS: Edition[] = [
  {
    id: "app",
    name: "Hygur App",
    kicker: "Clients",
    tagline: "Desktop and mobile",
    body: "The local twin in your pocket and on your desk. Ask, search and capture across Mac, Windows and your phone — everything stays on your machine.",
    badges: ["Free"],
    icon: MonitorSmartphone,
    cta: "Get the app",
  },
  {
    id: "server",
    name: "Hygur Server",
    kicker: "Self-host",
    tagline: "Standalone & headless",
    body: "The core that holds the data and the brain. Run the open-source binary on your own hardware — LAN, VPN or a box in the corner.",
    badges: ["Free", "Open source · AGPL"],
    icon: Server,
    cta: "Read the source",
  },
  {
    id: "cloud",
    name: "Hygur Cloud",
    kicker: "Managed",
    tagline: "Hosted Hygur Server — 29.90 €/mo launch price, then 35 €/mo",
    body: "A managed Hygur Server instance, one per account, running exclusively on EU servers. GPU inference runs at an EU provider that never trains on your data. We run and update it; you keep full control of your data and the model it talks to.",
    badges: CLOUD_OPEN
      ? ["Hosted", "29.90 €/mo · launch", "EU-only · no training"]
      : ["29.90 €/mo · launch", "EU-only · no training", "Coming soon"],
    icon: Cloud,
    cta: CLOUD_OPEN ? "Subscribe" : "Coming soon",
    // Closed: stay on the page (never open Stripe). Open: the Stripe payment link.
    href: CLOUD_OPEN ? CLOUD_SUBSCRIBE_URL : "#editions",
    featured: true,
  },
  {
    id: "marketplace",
    name: "Hygur Marketplace",
    kicker: "Ecosystem",
    tagline: "Connectors catalogue",
    body: "A growing catalogue of connectors — free and paid — to pull more of your world into Hygur, from mailboxes to calendars to custom sources.",
    badges: ["Free & paid"],
    icon: Blocks,
    cta: "Browse connectors",
  },
  {
    id: "teams",
    name: "Hygur Teams",
    kicker: "Soon",
    tagline: "Shared memory for teams & projects",
    body: "Everyone keeps their private twin. A project gets its own shared memory — a mesh of connected minds — where the team's mail, notes and decisions live together, with provenance and a daily brief.",
    badges: ["Soon"],
    icon: Network,
    cta: "Coming soon",
    href: "#editions",
  },
];
