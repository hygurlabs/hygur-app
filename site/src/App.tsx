import { useEffect } from "react";
import { useRoute, type Route } from "./lib/router";
import { Home } from "./components/Home";
import { LegalNotice } from "./components/legal/LegalNotice";
import { Privacy } from "./components/legal/Privacy";
import { Terms } from "./components/legal/Terms";
import { Connectors } from "./components/Connectors";
import { Subscribe } from "./components/Subscribe";
import { Engram } from "./components/Engram";

export default function App({ ssrRoute }: { ssrRoute?: Route }) {
  const client = useRoute();
  // The pre-render passes the target route explicitly (no window); the client
  // reads it from the location on mount.
  const route = ssrRoute ?? client.route;
  const anchor = ssrRoute ? undefined : client.anchor;

  // Legal routes open at the top; returning home with an anchor scrolls to the
  // matching section once it is rendered.
  useEffect(() => {
    if (route !== "home") {
      window.scrollTo(0, 0);
      return;
    }
    if (anchor) {
      requestAnimationFrame(() => {
        document.getElementById(anchor)?.scrollIntoView();
      });
    }
  }, [route, anchor]);

  if (route === "legal") return <LegalNotice />;
  if (route === "privacy") return <Privacy />;
  if (route === "terms") return <Terms />;
  if (route === "connectors") return <Connectors />;
  if (route === "subscribe") return <Subscribe />;
  if (route === "engram") return <Engram lang="en" />;
  if (route === "engram-fr") return <Engram lang="fr" />;
  return <Home />;
}
