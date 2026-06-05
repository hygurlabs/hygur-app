import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

// Self-hosted variable fonts — no third-party font calls, which keeps the
// landing page as privacy-respecting as the product it sells.
import "@fontsource-variable/fraunces";
import "@fontsource-variable/hanken-grotesk";
import "./index.css";

import App from "./App.tsx";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
