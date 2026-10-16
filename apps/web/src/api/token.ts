// The bearer token lives only in memory here; the session module persists it.
let current: string | null = null;
export const setToken = (token: string | null) => {
  current = token;
};
export const getToken = () => current;
