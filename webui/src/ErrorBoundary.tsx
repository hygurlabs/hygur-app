import { Component, type ErrorInfo, type ReactNode } from "react";
import { AlertTriangle } from "lucide-react";
import { reportClientError } from "./lib/errorReport";

interface State {
  error: Error | null;
  stack: string;
}

/** Top-level safety net: a render crash shows a recoverable screen instead of a
 *  blank page or a raw stack dump. Tauri release builds have no devtools, so
 *  without this a thrown error is invisible. Styled with the design tokens (CSS
 *  variables) via inline styles — dark-mode-safe, and no dependency on Tailwind
 *  utility generation in case the failure is style-related. Resets itself on
 *  route navigation (HashRouter → `hashchange`) so a crash on one view doesn't
 *  wedge the whole app. */
export class ErrorBoundary extends Component<{ children: ReactNode }, State> {
  state: State = { error: null, stack: "" };

  static getDerivedStateFromError(error: Error): Partial<State> {
    return { error };
  }

  componentDidMount() {
    window.addEventListener("hashchange", this.reset);
  }

  componentWillUnmount() {
    window.removeEventListener("hashchange", this.reset);
  }

  reset = () => {
    if (this.state.error) this.setState({ error: null, stack: "" });
  };

  componentDidCatch(error: Error, info: ErrorInfo) {
    this.setState({ error, stack: info.componentStack ?? "" });
    console.error("Hygur crashed:", error, info);
    reportClientError(error.message || "render crash", `${error.stack ?? ""}\n${info.componentStack ?? ""}`);
  }

  render() {
    const { error, stack } = this.state;
    if (error) {
      return (
        <div
          style={{
            minHeight: "100vh",
            boxSizing: "border-box",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            padding: 24,
            background: "var(--bg)",
            color: "var(--text)",
            fontFamily: "ui-sans-serif, system-ui, -apple-system, sans-serif",
          }}
        >
          <div style={{ width: "100%", maxWidth: 440 }}>
            <div
              style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                width: 44,
                height: 44,
                margin: "0 auto 16px",
                borderRadius: 12,
                color: "var(--danger)",
                background: "color-mix(in srgb, var(--danger) 12%, transparent)",
              }}
            >
              <AlertTriangle size={22} strokeWidth={1.9} />
            </div>
            <h1
              style={{
                fontFamily: "var(--font-display)",
                fontSize: 22,
                fontWeight: 600,
                letterSpacing: "-0.01em",
                textAlign: "center",
                margin: "0 0 8px",
              }}
            >
              Something went wrong
            </h1>
            <p
              style={{
                fontSize: 14,
                lineHeight: 1.55,
                textAlign: "center",
                color: "var(--muted)",
                margin: "0 0 20px",
              }}
            >
              Hygur hit an unexpected error and couldn't show this view. Reloading
              usually fixes it — your data is safe.
            </p>
            <div style={{ display: "flex", justifyContent: "center" }}>
              <button
                onClick={() => window.location.reload()}
                style={{
                  cursor: "pointer",
                  borderRadius: 8,
                  border: "none",
                  padding: "9px 18px",
                  fontSize: 14,
                  fontWeight: 500,
                  color: "#fff",
                  background: "var(--accent)",
                }}
              >
                Reload
              </button>
            </div>
            <details style={{ marginTop: 22 }}>
              <summary
                style={{
                  cursor: "pointer",
                  fontSize: 12.5,
                  color: "var(--faint)",
                  textAlign: "center",
                }}
              >
                Technical details
              </summary>
              <pre
                style={{
                  marginTop: 10,
                  padding: 12,
                  maxHeight: 320,
                  overflow: "auto",
                  fontFamily: "var(--font-mono)",
                  fontSize: 12,
                  lineHeight: 1.5,
                  whiteSpace: "pre-wrap",
                  wordBreak: "break-word",
                  color: "var(--muted)",
                  background: "var(--surface-2)",
                  border: "1px solid var(--border)",
                  borderRadius: 8,
                }}
              >
                {error.message}
                {error.stack ? `\n\n${error.stack}` : ""}
                {stack ? `\n\nComponent stack:${stack}` : ""}
              </pre>
            </details>
          </div>
        </div>
      );
    }
    return this.props.children;
  }
}
