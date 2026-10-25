import { useSyncExternalStore, type AnchorHTMLAttributes, type MouseEvent } from "react";
import { matchRoute } from "./routes.ts";

const NAV = "admin:navigate";

function subscribe(notify: () => void) {
  window.addEventListener("popstate", notify);
  window.addEventListener(NAV, notify);
  return () => {
    window.removeEventListener("popstate", notify);
    window.removeEventListener(NAV, notify);
  };
}

/** Moves inside the console. Leaving for another origin is `window.location.assign`, never this. */
export function navigate(to: string, replace = false) {
  if (to === window.location.pathname + window.location.search) return;
  if (replace) window.history.replaceState(null, "", to);
  else window.history.pushState(null, "", to);
  window.dispatchEvent(new Event(NAV));
  window.scrollTo(0, 0);
}

export const usePath = () => useSyncExternalStore(subscribe, () => window.location.pathname);
export const useRoute = () => matchRoute(usePath());

/** A same-origin link that navigates without reloading. */
export function Link({ to, onClick, ...rest }: { to: string } & AnchorHTMLAttributes<HTMLAnchorElement>) {
  const handle = (e: MouseEvent<HTMLAnchorElement>) => {
    onClick?.(e);
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    navigate(to);
  };
  return <a href={to} onClick={handle} {...rest} />;
}
