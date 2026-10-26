import { useEffect, useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { isNavigableUrl, readNavContext } from "@kongken/account/navigation";
import type { NavigationTarget, ReturnFrom } from "@kongken/account/api";
import { account } from "./rpc";

/**
 * Where an account step ends. `none`: OzID was opened directly. `ok`: the BFF accepted
 * the app ID and path. `refused` and `unavailable`: the address could not be confirmed;
 * in both cases the only safe place to end is OzID itself.
 */
export type TargetState =
  | { status: "none" }
  | { status: "resolving" }
  | { status: "ok"; target: NavigationTarget }
  | { status: "refused" }
  | { status: "unavailable" };

/**
 * Resolves the `app_id` / `return_path` of the query through the BFF's registry. The
 * page never builds a return address itself: it only ever uses `target.returnUrl`.
 */
export function useTarget(search: string): TargetState {
  const [state, setState] = useState<TargetState>(() => initial(search));
  useEffect(() => {
    const ctx = readNavContext(search);
    if (ctx.kind !== "context") {
      setState(initial(search));
      return;
    }
    let live = true;
    setState({ status: "resolving" });
    account
      .resolveNavigation(ctx.appId, ctx.returnPath)
      .then((target) => {
        if (!live) return;
        // The server's answer is the only thing trusted, but it must still be a web address.
        setState(isNavigableUrl(target.returnUrl) ? { status: "ok", target } : { status: "refused" });
      })
      .catch((e: unknown) => {
        if (!live) return;
        // INVALID_ARGUMENT and FAILED_PRECONDITION are the BFF's refusals; anything else is an outage.
        const code = ConnectError.from(e).code;
        setState({ status: code === Code.InvalidArgument || code === Code.FailedPrecondition ? "refused" : "unavailable" });
      });
    return () => {
      live = false;
    };
  }, [search]);
  return state;
}

function initial(search: string): TargetState {
  const ctx = readNavContext(search);
  return ctx.kind === "none" ? { status: "none" } : ctx.kind === "invalid" ? { status: "refused" } : { status: "resolving" };
}

/** Sends the browser to a target the BFF accepted. A full navigation: the target is another origin. */
export function goToTarget(target: NavigationTarget) {
  if (isNavigableUrl(target.returnUrl)) window.location.assign(target.returnUrl);
}

/**
 * The return context a mail request binds to its challenge: only what the BFF accepted, and
 * none otherwise (opened directly, refused, or the BFF could not answer). The mail itself
 * always lands at OzID; this only says where the user can be sent afterwards.
 */
export function fromTarget(target: TargetState): ReturnFrom | null {
  return target.status === "ok" ? { appId: target.target.appId, returnPath: target.target.returnPath } : null;
}
