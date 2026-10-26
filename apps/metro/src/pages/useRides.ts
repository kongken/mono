import { useCallback, useEffect, useMemo, useState } from "react";
import type { Line } from "@kongken/api-client/bff/metro/v1/catalog_pb";
import { rider, write } from "@/lib/rpc";
import { describeError, isUnauthenticated } from "@/lib/errors";
import { useSession } from "@/lib/session";

/**
 * The signed-in rider's marked lines, with mark and unmark. Anonymous visitors
 * get an empty set, and marking sends them to sign in.
 */
export function useRides() {
  const { session, signIn, refresh } = useSession();
  const [codes, setCodes] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState<string | undefined>();
  const [error, setError] = useState<string | undefined>();
  const signedIn = session.status === "signed-in";

  useEffect(() => {
    if (!signedIn) {
      setCodes(new Set());
      return;
    }
    let current = true;
    rider.listMyLines({}).then(
      (res) => current && setCodes(new Set(res.cities.flatMap((c) => c.lines.map((l) => l.lineCode)))),
      (e) => {
        if (!current) return;
        setError(describeError(e));
        if (isUnauthenticated(e)) void refresh();
      },
    );
    return () => {
      current = false;
    };
  }, [signedIn, refresh]);

  const toggle = useCallback(
    async (line: Line) => {
      if (!signedIn) {
        // Only a known "not signed in" sends anybody to sign in; a check that could not be made is retried instead.
        if (session.status === "unavailable") {
          setError("暂时无法确认登录状态，请稍后重新检查。");
          void refresh();
          return;
        }
        signIn();
        return;
      }
      setBusy(line.code);
      setError(undefined);
      try {
        if (codes.has(line.code)) {
          await write(() => rider.unmarkLine({ lineCode: line.code }));
          setCodes((s) => new Set([...s].filter((c) => c !== line.code)));
        } else {
          await write(() => rider.markLine({ lineCode: line.code }));
          setCodes((s) => new Set(s).add(line.code));
        }
      } catch (e) {
        setError(describeError(e));
        // Refused because the session ended or is another account's now: nothing was written; ask who it is.
        if (isUnauthenticated(e)) void refresh();
      } finally {
        setBusy(undefined);
      }
    },
    [signedIn, session.status, codes, signIn, refresh],
  );

  return useMemo(() => ({ codes, busy, error, toggle, signedIn }), [codes, busy, error, toggle, signedIn]);
}
