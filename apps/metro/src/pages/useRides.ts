import { useCallback, useEffect, useMemo, useState } from "react";
import type { Line } from "@kongken/api-client/bff/metro/v1/catalog_pb";
import { rider, write } from "@/lib/rpc";
import { describeError, isUnauthenticated } from "@/lib/errors";
import { useSession } from "@/lib/session";

/** Owns the ride snapshot and editor selection; opening an editor never writes. */
export function useRides() {
  const { session, signIn, refresh } = useSession();
  // Unknown is distinct from an empty archive: do not offer overwrites until the read succeeds.
  const [records, setRecords] = useState<Map<string, number>>();
  const [editing, setEditing] = useState<Line>();
  const [error, setError] = useState<string>();
  const [attempt, setAttempt] = useState(0);
  const signedIn = session.status === "signed-in";

  useEffect(() => {
    if (!signedIn) return;
    let current = true;
    rider.listMyLines({}).then(
      (res) => {
        if (!current) return;
        setRecords(new Map(res.cities.flatMap((c) => c.lines.map((l) => [l.lineCode, l.riddenYear] as const))));
        setError(undefined);
      },
      (e) => {
        if (!current) return;
        setError(describeError(e));
        if (isUnauthenticated(e)) void refresh();
      },
    );
    return () => { current = false; };
  }, [signedIn, refresh, attempt]);

  const edit = useCallback((line: Line) => {
    if (session.status === "anonymous") return signIn();
    if (session.status === "unavailable") {
      setError("暂时无法确认登录状态，请稍后重新检查。");
      void refresh();
      return;
    }
    if (signedIn && records) setEditing(line);
  }, [session.status, signedIn, records, signIn, refresh]);

  const save = async (year: number | undefined) => {
    if (!editing) return;
    try {
      await write(() => rider.markLine({ lineCode: editing.code, riddenYear: year }));
      setRecords((r) => new Map(r).set(editing.code, year ?? 0));
    } catch (e) {
      if (isUnauthenticated(e)) void refresh();
      throw e;
    }
  };

  const remove = async () => {
    if (!editing) return;
    try {
      await write(() => rider.unmarkLine({ lineCode: editing.code }));
      setRecords((r) => {
        const next = new Map(r);
        next.delete(editing.code);
        return next;
      });
    } catch (e) {
      if (isUnauthenticated(e)) void refresh();
      throw e;
    }
  };

  const codes = useMemo(() => new Set(records?.keys()), [records]);
  return {
    codes, records, error, signedIn, editing, edit, save, remove,
    close: () => setEditing(undefined),
    retry: () => { setError(undefined); setAttempt((n) => n + 1); },
    ready: signedIn && records !== undefined,
    disabled: session.status === "loading" || (signedIn && records === undefined),
  };
}
