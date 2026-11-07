import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import PrivacyPage from "./Privacy";
import "./index.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <PrivacyPage />
  </StrictMode>,
);
