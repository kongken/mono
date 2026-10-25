import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { captureLandingToken } from "@kongken/account/fragment";
import { AccountLanding } from "@kongken/account/landing";
import "./index.css";
import App from "./App.tsx";
import { account } from "./lib/rpc.ts";

// The links in account mails open /account/verify-email and /account/reset-password with
// the one-time token in the URL fragment. It is moved into memory and removed from the
// address bar before anything else runs; those pages do not start the app, so no session
// or catalogue request is made, and they use no third-party script.
const landing = captureLandingToken();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    {landing ? <AccountLanding kind={landing.kind} api={account} appName="地铁足迹" loginHref="/login" /> : <App />}
  </StrictMode>,
);
