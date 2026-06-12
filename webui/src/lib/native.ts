// Typed view over the JS↔Swift bridge the native WKWebView shell injects as
// `window.HygurNative`. Every call degrades to a safe no-op when the page is
// opened in a plain browser (Vite dev, debugging) so the web app never crashes
// for lacking a host — callers check `native.available` to adapt the UI.

export interface NativeCalendar {
  id: string;
  title: string;
  color: string;
  writable: boolean;
}

export interface NativeEvent {
  title: string;
  start: string; // ISO8601
  end: string; // ISO8601
  location: string;
  notes: string;
  calendarId: string;
  calendarTitle: string;
  attendees: string[];
  allDay: boolean;
}

/** Live speech-to-text callback: partial transcripts as the user dictates,
 *  with `isFinal=true` when recording stops. */
export type DictationListener = (text: string, isFinal: boolean) => void;

interface HygurNativeAPI {
  available: boolean;
  onDictation: DictationListener | null;
  ping(): Promise<unknown>;
  calendar: {
    authorize(): Promise<boolean>;
    listCalendars(): Promise<NativeCalendar[]>;
    listEvents(hours: number): Promise<NativeEvent[]>;
    setEnabled(enabled: boolean, calendars: string[]): Promise<unknown>;
  };
  dictation: {
    start(): Promise<boolean>;
    stop(): Promise<string>;
  };
  prefs: {
    getBool(key: string): Promise<boolean>;
    setBool(key: string, value: boolean): Promise<unknown>;
  };
  perms: {
    status(): Promise<Record<string, string>>;
  };
  openExternal(url: string): Promise<unknown>;
  notify(title: string, body: string): Promise<unknown>;
  // Added by newer shells; optional so older hosts degrade to browser fallbacks.
  download?(filename: string, content: string): Promise<boolean>;
  print?(): Promise<unknown>;
}

declare global {
  interface Window {
    HygurNative?: HygurNativeAPI;
  }
}

// ── Web Speech dictation fallback (Tauri WebKit / Chrome / Safari) ───────────
// Used when there's no native bridge so the mic works in Tauri and the browser.
interface SpeechAlt {
  transcript: string;
}
interface SpeechResult {
  isFinal: boolean;
  0: SpeechAlt;
}
interface SpeechResultList {
  length: number;
  [i: number]: SpeechResult;
}
interface SpeechEvent {
  resultIndex: number;
  results: SpeechResultList;
}
interface WebSpeechRecognition {
  continuous: boolean;
  interimResults: boolean;
  lang: string;
  onresult: ((e: SpeechEvent) => void) | null;
  onend: (() => void) | null;
  onerror: (() => void) | null;
  start(): void;
  stop(): void;
}
type SpeechRecognitionCtor = new () => WebSpeechRecognition;

const webDictation = (() => {
  const w = window as unknown as {
    SpeechRecognition?: SpeechRecognitionCtor;
    webkitSpeechRecognition?: SpeechRecognitionCtor;
  };
  const Ctor = w.SpeechRecognition ?? w.webkitSpeechRecognition;
  if (!Ctor) return null;
  let rec: WebSpeechRecognition | null = null;
  let listener: DictationListener | null = null;
  let finalText = "";
  return {
    start(): Promise<boolean> {
      try {
        finalText = "";
        rec = new Ctor();
        rec.continuous = true;
        rec.interimResults = true;
        rec.lang = navigator.language || "en-US";
        rec.onresult = (e: SpeechEvent) => {
          let interim = "";
          for (let i = e.resultIndex; i < e.results.length; i++) {
            const r = e.results[i];
            if (r.isFinal) finalText += r[0].transcript;
            else interim += r[0].transcript;
          }
          listener?.(`${finalText}${interim}`.trim(), false);
        };
        rec.onend = () => listener?.(finalText.trim(), true);
        rec.onerror = () => listener?.(finalText.trim(), true);
        rec.start();
        return Promise.resolve(true);
      } catch {
        return Promise.resolve(false);
      }
    },
    stop(): Promise<string> {
      try {
        rec?.stop();
      } catch {
        /* ignore */
      }
      return Promise.resolve(finalText.trim());
    },
    setListener(fn: DictationListener | null) {
      listener = fn;
    },
  };
})();

export const native = {
  get available(): boolean {
    return Boolean(window.HygurNative?.available);
  },
  /** STT availability: the native bridge, or the Web Speech API in Tauri/browser. */
  get dictationAvailable(): boolean {
    return Boolean(window.HygurNative?.dictation) || webDictation !== null;
  },
  calendar: {
    authorize: (): Promise<boolean> =>
      window.HygurNative?.calendar.authorize() ?? Promise.resolve(false),
    listCalendars: (): Promise<NativeCalendar[]> =>
      window.HygurNative?.calendar.listCalendars() ?? Promise.resolve([]),
    listEvents: (hours: number): Promise<NativeEvent[]> =>
      window.HygurNative?.calendar.listEvents(hours) ?? Promise.resolve([]),
    /** Persists the meeting-briefing calendar selection natively. */
    setEnabled: (enabled: boolean, calendars: string[]): Promise<unknown> =>
      window.HygurNative?.calendar.setEnabled(enabled, calendars) ??
      Promise.resolve(),
  },
  dictation: {
    start: (): Promise<boolean> =>
      window.HygurNative?.dictation.start() ??
      webDictation?.start() ??
      Promise.resolve(false),
    stop: (): Promise<string> =>
      window.HygurNative?.dictation.stop() ??
      webDictation?.stop() ??
      Promise.resolve(""),
    /** Subscribe to live partials. Returns an unsubscribe function. */
    listen: (fn: DictationListener): (() => void) => {
      if (window.HygurNative) {
        window.HygurNative.onDictation = fn;
        return () => {
          if (window.HygurNative) window.HygurNative.onDictation = null;
        };
      }
      if (webDictation) {
        webDictation.setListener(fn);
        return () => webDictation.setListener(null);
      }
      return () => {};
    },
  },
  prefs: {
    getBool: (key: string): Promise<boolean> => {
      if (window.HygurNative) return window.HygurNative.prefs.getBool(key);
      // Web/Tauri fallback: persist in localStorage so toggles survive reloads.
      try {
        return Promise.resolve(localStorage.getItem(`pref.${key}`) === "1");
      } catch {
        return Promise.resolve(false);
      }
    },
    setBool: (key: string, value: boolean): Promise<unknown> => {
      if (window.HygurNative) return window.HygurNative.prefs.setBool(key, value);
      try {
        localStorage.setItem(`pref.${key}`, value ? "1" : "0");
      } catch {
        /* storage unavailable — pref won't persist, but the UI still toggles */
      }
      return Promise.resolve();
    },
  },
  perms: {
    status: (): Promise<Record<string, string>> =>
      window.HygurNative?.perms.status() ?? Promise.resolve({}),
  },
  /** Opens a URL in the default browser (native), or a new tab in a plain browser. */
  openExternal: (url: string): Promise<unknown> => {
    if (window.HygurNative) return window.HygurNative.openExternal(url);
    window.open(url, "_blank", "noopener");
    return Promise.resolve();
  },
  /** Native shells post a system banner; web/Tauri fall back to the Web
   *  Notifications API (requesting permission on first use). */
  notify: (title: string, body: string): Promise<unknown> => {
    if (window.HygurNative) return window.HygurNative.notify(title, body);
    try {
      if (typeof Notification === "undefined") return Promise.resolve();
      const show = () => {
        try {
          new Notification(title, { body });
        } catch {
          /* construction can throw on some platforms — ignore */
        }
      };
      if (Notification.permission === "granted") show();
      else if (Notification.permission !== "denied") {
        void Notification.requestPermission().then((p) => {
          if (p === "granted") show();
        });
      }
    } catch {
      /* Notifications unavailable — silent */
    }
    return Promise.resolve();
  },
  /** Saves a text file. Native shells show a save panel; a plain browser
   *  triggers a blob download. */
  download: (filename: string, mime: string, content: string): Promise<boolean> => {
    if (window.HygurNative?.download) {
      return window.HygurNative.download(filename, content);
    }
    const blob = new Blob([content], { type: mime });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    a.remove();
    window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    return Promise.resolve(true);
  },
  /** Prints the current page. Native shells use the macOS print panel;
   *  a plain browser uses window.print(). */
  print: (): Promise<unknown> => {
    if (window.HygurNative?.print) return window.HygurNative.print();
    window.print();
    return Promise.resolve();
  },
};
