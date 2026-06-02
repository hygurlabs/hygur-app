import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { HashRouter } from "react-router-dom";
import "./index.css";
import App from "./App.tsx";
import { Onboarding } from "./onboarding/Onboarding";
import { isOnboardingComplete } from "./lib/onboarding";

// HashRouter: the shell always loads at `/`, so client routes live in the hash
// and need no server-side fallback — keeps the Go handler trivial.
const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 30_000, refetchOnWindowFocus: false, retry: 1 },
  },
});

/** Gates the app behind first-run onboarding. `done === null` is the brief
 *  async check; the WebShellView paints its own "Starting Hygur…" cover until
 *  the page is ready, so a blank frame here is invisible to the user. */
function Root() {
  const [done, setDone] = useState<boolean | null>(null);

  useEffect(() => {
    let cancelled = false;
    void isOnboardingComplete().then((v) => {
      if (!cancelled) setDone(v);
    });
    return () => {
      cancelled = true;
    };
  }, []);

  if (done === null) return null;
  if (!done) return <Onboarding onComplete={() => setDone(true)} />;
  return <App />;
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <HashRouter>
        <Root />
      </HashRouter>
    </QueryClientProvider>
  </StrictMode>,
);
