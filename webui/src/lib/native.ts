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

export const native = {
  get available(): boolean {
    return Boolean(window.HygurNative?.available);
  },
  /** On-device STT availability (the bridge exposes the engine). */
  get dictationAvailable(): boolean {
    return Boolean(window.HygurNative?.dictation);
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
      window.HygurNative?.dictation.start() ?? Promise.resolve(false),
    stop: (): Promise<string> =>
      window.HygurNative?.dictation.stop() ?? Promise.resolve(""),
    /** Subscribe to live partials. Returns an unsubscribe function. */
    listen: (fn: DictationListener): (() => void) => {
      if (!window.HygurNative) return () => {};
      window.HygurNative.onDictation = fn;
      return () => {
        if (window.HygurNative) window.HygurNative.onDictation = null;
      };
    },
  },
  prefs: {
    getBool: (key: string): Promise<boolean> =>
      window.HygurNative?.prefs.getBool(key) ?? Promise.resolve(false),
    setBool: (key: string, value: boolean): Promise<unknown> =>
      window.HygurNative?.prefs.setBool(key, value) ?? Promise.resolve(),
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
  notify: (title: string, body: string): Promise<unknown> =>
    window.HygurNative?.notify(title, body) ?? Promise.resolve(),
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
