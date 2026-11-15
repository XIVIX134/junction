import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { applyTheme } from "./prefs";
import "./theme.css";
import "./app.css";
import "./motion.css";
import "./motion2.css";

applyTheme();
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
