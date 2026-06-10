import { renderToString } from "react-dom/server";
import App from "./App";

// SSR entry for the static pre-render (build:ssg). It imports NO CSS or fonts —
// those stay client-only in main.tsx — so it produces just the markup (Tailwind
// class names), which the client build's linked stylesheet styles. With no
// window at render time, App renders the landing route (see router.ts guard).
export function render(): string {
  return renderToString(<App />);
}
