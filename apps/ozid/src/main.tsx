import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./index.css";
import App from "./App.tsx";
import { configError } from "./lib/rpc.ts";

// A missing or malformed public build setting is a deployment mistake: say so plainly
// instead of rendering a page that cannot sign anybody in.
function ConfigError({ message }: { message: string }) {
  return (
    <main className="mx-auto max-w-xl p-6" role="alert">
      <h1 className="text-lg font-semibold">OzID 没有正确配置</h1>
      <p className="mt-2 text-sm">{message}</p>
    </main>
  );
}

createRoot(document.getElementById("root")!).render(<StrictMode>{configError ? <ConfigError message={configError} /> : <App />}</StrictMode>);
