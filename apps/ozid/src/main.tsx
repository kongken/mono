import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { navQuery } from "@kongken/account/navigation";
import { captureLandingToken } from "@kongken/account/fragment";
import { AccountLanding } from "@kongken/account/landing";
import { Notice } from "@kongken/account/ui";
import "./index.css";
import App from "./App.tsx";
import { account, configError } from "./lib/rpc.ts";

// The links in account mails open /account/verify-email and /account/reset-password with
// the one-time token in the URL fragment. It is moved into memory and removed from the
// address bar before anything else runs (and before this module reads or renders anything
// else); those pages do not start the app, so no session or navigation request is made, and
// they use no third-party script. Off those paths nothing is touched.
const landing = captureLandingToken();

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

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    {configError ? (
      <ConfigError message={configError} />
    ) : landing ? (
      <AccountLanding
        kind={landing.kind}
        api={account}
        appName="OzID"
        // Finishing registers or resets and signs nobody in: the way on is the sign-in page,
        // with the return context the BFF just re-validated (none when it is no longer allowed).
        loginHref={(back) => `/login${back ? navQuery(back) : ""}`}
        aside={<Notice kind="info">完成后不会自动登录，也不会改变这个浏览器上已有的登录。</Notice>}
      />
    ) : (
      <App />
    )}
  </StrictMode>,
);
