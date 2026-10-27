import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { captureLandingToken } from "@kongken/account/fragment";
import { MailHandOff } from "@kongken/account/handoff";
import "@fontsource-variable/space-grotesk";
import "@fontsource-variable/hanken-grotesk";
import "@fontsource-variable/jetbrains-mono";
import "./index.css";
import App from "./App.tsx";
import { ozid } from "./lib/account-center.ts";

// The links in account mails sent before OzID open /account/verify-email and /account/reset-password with
// the one-time token in the URL fragment. It is moved into memory and removed from the address bar before
// anything else runs; those pages do not start the app, so no session or catalogue request is made, they
// use no third-party script and they have no form: they hand the browser and the token over to OzID, which
// completes the mail or asks for a new one (new mails link to OzID itself).
const landing = captureLandingToken();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    {landing ? <MailHandOff kind={landing.kind} appName="地铁足迹" ozidOrigin={ozid.enabled ? ozid.origin : null} /> : <App />}
  </StrictMode>,
);
