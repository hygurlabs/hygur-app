import { Component, type ErrorInfo, type ReactNode } from "react";
import { reportClientError } from "./lib/errorReport";

interface State {
  error: Error | null;
  stack: string;
}

/** Top-level safety net: a render crash shows the error on screen instead of a
 *  blank page. Tauri release builds have no devtools, so without this a thrown
 *  error is invisible. Inline styles only (no Tailwind dependency, in case the
 *  failure is style-related). */
export class ErrorBoundary extends Component<{ children: ReactNode }, State> {
  state: State = { error: null, stack: "" };

  static getDerivedStateFromError(error: Error): Partial<State> {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    this.setState({ error, stack: info.componentStack ?? "" });
    console.error("Hygur crashed:", error, info);
    reportClientError(error.message || "render crash", `${error.stack ?? ""}\n${info.componentStack ?? ""}`);
  }

  render() {
    if (this.state.error) {
      return (
        <div
          style={{
            padding: 24,
            fontFamily: "ui-monospace, monospace",
            fontSize: 13,
            lineHeight: 1.5,
            color: "#d33",
            background: "#1a1a1a",
            whiteSpace: "pre-wrap",
            wordBreak: "break-word",
            overflow: "auto",
            height: "100vh",
            boxSizing: "border-box",
          }}
        >
          <strong>Hygur — une erreur est survenue</strong>
          {"\n\n"}
          {this.state.error.message}
          {"\n\n"}
          {this.state.error.stack}
          {this.state.stack ? `\n\nPile des composants :${this.state.stack}` : ""}
        </div>
      );
    }
    return this.props.children;
  }
}
