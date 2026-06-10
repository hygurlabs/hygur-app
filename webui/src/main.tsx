import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { HashRouter } from "react-router-dom";
import "./index.css";
import { Root } from "./Root";
import { ErrorBoundary } from "./ErrorBoundary";
import { installErrorReporting } from "./lib/errorReport";

// Capture uncaught errors + unhandled rejections (cloud sessions report them to
// the operator console; local-first instances keep them on the machine).
installErrorReporting();

// HashRouter: the shell always loads at `/`, so client routes live in the hash
// and need no server-side fallback — keeps the Go handler trivial.
const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 30_000, refetchOnWindowFocus: false, retry: 1 },
  },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ErrorBoundary>
      <QueryClientProvider client={queryClient}>
        <HashRouter>
          <Root />
        </HashRouter>
      </QueryClientProvider>
    </ErrorBoundary>
  </StrictMode>,
);
