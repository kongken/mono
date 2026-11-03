import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import ExtensionsPage from "./Extensions";
import "./index.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ExtensionsPage />
  </StrictMode>,
);
