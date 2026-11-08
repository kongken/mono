import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App.tsx";
import "./index.css";

const root = document.getElementById("root");
if (root === null) throw new Error("@kongken/timeline cannot start: #root is missing");

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
