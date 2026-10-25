/**
 * A validity boundary for asynchronous reads whose answers are written into state.
 *
 * Every read starts with `begin()` and asks the returned function before it writes anything.
 * An answer counts only while no newer read has begun and nothing has called `invalidate()`
 * (a sign-out, an identity change, an unmount). So an older success cannot overwrite a newer
 * "not signed in", and an older failure cannot overwrite a newer valid identity or list.
 */
export type Epoch = {
  /** Starts a read; the result says whether that read is still the newest valid one. */
  begin: () => () => boolean;
  /** Makes every read started so far stale. */
  invalidate: () => void;
};

export function createEpoch(): Epoch {
  let current = 0;
  return {
    begin: () => {
      const mine = ++current;
      return () => mine === current;
    },
    invalidate: () => {
      current += 1;
    },
  };
}
