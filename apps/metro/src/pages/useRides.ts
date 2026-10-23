import { useCallback, useEffect, useMemo, useState } from "react";
import type { Line } from "@kongken/api-client/bff/metro/v1/catalog_pb";
import { rider, write } from "@/lib/rpc";
import { describeError } from "@/lib/errors";
import { useSession } from "@/lib/session";

/**
 * The signed-in rider's marked lines, with mark and unmark. Anonymous visitors
 * get an empty set, and marking sends them to sign in.
 */
export function useRides() {
  const { session, signIn } = useSession();
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
      (e) => current && setError(describeError(e)),
    );
    return () => {
      current = false;
    };
  }, [signedIn]);

  const toggle = useCallback(
    async (line: Line) => {
      if (!signedIn) {
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
      } finally {
        setBusy(undefined);
      }
    },
    [signedIn, codes, signIn],
  );

  return useMemo(() => ({ codes, busy, error, toggle, signedIn }), [codes, busy, error, toggle, signedIn]);
}
