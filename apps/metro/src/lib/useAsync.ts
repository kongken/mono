import { useEffect, useRef, useState } from "react";

export type Async<T> = { status: "loading" } | { status: "error"; error: unknown } | { status: "ready"; value: T };

/** Runs load whenever deps change; reload() runs it again. A stale result never overwrites a newer one. */
export function useAsync<T>(load: () => Promise<T>, deps: unknown[]): [Async<T>, () => void] {
  const [state, setState] = useState<Async<T>>({ status: "loading" });
  const [nonce, setNonce] = useState(0);
  const loader = useRef(load);
  loader.current = load;
  useEffect(() => {
    let current = true;
    setState((s) => (s.status === "ready" ? s : { status: "loading" }));
    loader.current().then(
      (value) => current && setState({ status: "ready", value }),
      (error) => current && setState({ status: "error", error }),
    );
    return () => {
      current = false;
    };
  }, [nonce, ...deps]);
  return [state, () => setNonce((n) => n + 1)];
}
