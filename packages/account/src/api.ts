import { createClient, type Transport } from "@connectrpc/connect";
import { timestampDate, type Timestamp } from "@bufbuild/protobuf/wkt";
import {
  AccountDeletionState,
  AuthService,
  UserService,
  type AccountDeletion,
  type User,
} from "@kongken/api-client/bff/v1/auth_pb";

/** The cross-product face of an account: no email, no roles. */
export type AccountUser = { id: string; displayName: string; avatarUrl: string };

/** The signed-in owner's own account screen: the only place the login email appears. */
export type AccountProfile = {
  user: AccountUser;
  email: string;
  roles: string[];
  authenticatedAt: Date | null;
  sessionExpiresAt: Date | null;
};

export type SessionInfo = {
  id: string;
  current: boolean;
  createdAt: Date | null;
  lastUsedAt: Date | null;
  expiresAt: Date | null;
};

export type DeletionStatus = {
  /** True once the account and every listed product's data are gone. */
  completed: boolean;
  mutationId: string;
  products: string[];
  completedProducts: string[];
};

/** A navigation target the BFF accepted: the only address a page may send the browser to. */
export type NavigationTarget = {
  appId: string;
  returnPath: string;
  /** The registered origin of the app plus the path. */
  returnUrl: string;
  ozidOrigin: string;
};

/**
 * Where a mail request came from, so the user can find the way back after finishing the
 * flow in another browser. The BFF validates it against the navigation registry and binds
 * it to the challenge; it never decides which site the mail links to (always OzID).
 */
export type ReturnFrom = { appId: string; returnPath?: string };

/** The validated context a finished mail flow hands back. The BFF has re-checked it just now. */
export type ReturnContext = { appId: string; returnPath: string };

const returnOf = (res: { appId: string; returnPath: string }): ReturnContext | null =>
  res.appId ? { appId: res.appId, returnPath: res.returnPath } : null;

export type AccountApiOptions = {
  /** The browser transport of this app: cookies included, CSRF header added by its interceptors. */
  transport: Transport;
  /**
   * The app this client belongs to (`trip-map`, `metro`, `ozid`). Mail requests name it as
   * their return context unless the caller passes another one (or null for none).
   */
  appId: string;
  /** Fetches a fresh CSRF token before a state-changing call; each app owns where the token is kept. */
  ensureCsrf: () => Promise<void>;
};

const date = (t: Timestamp | undefined) => (t ? timestampDate(t) : null);

export function toAccountUser(u: User | undefined): AccountUser {
  if (!u?.id) throw new Error("incomplete user");
  return { id: u.id, displayName: u.displayName, avatarUrl: u.avatarUrl };
}

function toDeletion(d: AccountDeletion | undefined): DeletionStatus {
  return {
    completed: d?.state === AccountDeletionState.COMPLETED,
    mutationId: d?.mutationId ?? "",
    products: d?.products ?? [],
    completedProducts: d?.completedProducts ?? [],
  };
}

/**
 * The browser account client both apps use. Every state-changing call, anonymous ones
 * included, first obtains a CSRF token (the BFF double-submit check). Passwords, tokens
 * and the email are only ever passed straight into the request body.
 */
export function createAccountApi({ transport, appId, ensureCsrf }: AccountApiOptions) {
  const auth = createClient(AuthService, transport);
  const users = createClient(UserService, transport);
  const write = async <T>(call: () => Promise<T>): Promise<T> => {
    await ensureCsrf();
    return call();
  };

  return {
    appId,
    /** `from`: the return context to bind (default: this app itself; null: none). */
    requestRegistration: (email: string, from?: ReturnFrom | null) => {
      const ctx = from === undefined ? { appId } : from;
      return write(() => auth.requestRegistration({ email, appId: ctx?.appId ?? "", returnPath: ctx?.returnPath ?? "" })).then(() => undefined);
    },
    /** Resolves with the return context bound to the link, or null when there is none (or it is no longer allowed). */
    completeRegistration: async (token: string, password: string, displayName: string): Promise<ReturnContext | null> =>
      returnOf(await write(() => auth.completeRegistration({ token, password, displayName }))),
    /** Sets the session Cookie; the response carries the profile and expiry, never a token. */
    login: async (email: string, password: string) => {
      const res = await write(() => auth.loginWithEmail({ email, password }));
      return { user: toAccountUser(res.user), expiresAt: date(res.expiresAt) };
    },
    requestPasswordReset: (email: string, from?: ReturnFrom | null) => {
      const ctx = from === undefined ? { appId } : from;
      return write(() => auth.requestPasswordReset({ email, appId: ctx?.appId ?? "", returnPath: ctx?.returnPath ?? "" })).then(() => undefined);
    },
    resetPassword: async (token: string, newPassword: string): Promise<ReturnContext | null> =>
      returnOf(await write(() => auth.resetPassword({ token, newPassword }))),

    /**
     * Asks the BFF whether an app ID and return path are registered. Anonymous and
     * read-only: it needs no session or CSRF token and establishes nothing. A refusal
     * (unknown app, path not allowed, registry not configured) throws; the caller must
     * then not navigate anywhere but stay in OzID.
     */
    resolveNavigation: async (targetAppId: string, returnPath: string): Promise<NavigationTarget> => {
      const res = await auth.resolveNavigation({ appId: targetAppId, returnPath });
      return { appId: res.appId, returnPath: res.returnPath, returnUrl: res.returnUrl, ozidOrigin: res.ozidOrigin };
    },

    me: async (): Promise<AccountProfile> => {
      const res = await auth.me({});
      return {
        user: toAccountUser(res.user),
        email: res.email,
        roles: res.roles,
        authenticatedAt: date(res.authenticatedAt),
        sessionExpiresAt: date(res.sessionExpiresAt),
      };
    },
    listSessions: async (): Promise<SessionInfo[]> =>
      (await auth.listSessions({})).sessions.map((s) => ({
        id: s.id,
        current: s.current,
        createdAt: date(s.createdAt),
        lastUsedAt: date(s.lastUsedAt),
        expiresAt: date(s.expiresAt),
      })),
    revokeSession: (id: string) => write(() => auth.revokeSession({ id })).then(() => undefined),
    /**
     * Signs out every other device in one atomic, idempotent step and keeps this session. It
     * neither changes the password nor ends management tokens. Resolves with how many sessions
     * this call ended (0 on a repeat).
     */
    revokeOtherSessions: async (): Promise<number> => (await write(() => auth.revokeOtherSessions({}))).revoked,
    logout: () => write(() => auth.logout({})).then(() => undefined),
    reauthenticate: (password: string) => write(() => auth.reauthenticate({ password })).then(() => undefined),
    changePassword: (currentPassword: string, newPassword: string) =>
      write(() => auth.changePassword({ currentPassword, newPassword })).then(() => undefined),
    updateProfile: async (displayName: string, avatarUrl: string) => {
      const res = await write(() => users.updateMyProfile({ displayName, avatarUrl }));
      return toAccountUser(res.user);
    },

    /** Starts (or, with the same mutation ID, continues) closing the shared account. Needs a password check within 15 minutes. */
    deleteAccount: async (mutationId: string) =>
      toDeletion((await write(() => auth.deleteAccount({ confirmation: "DELETE", mutationId }))).deletion),
    getDeletion: async () => toDeletion((await auth.getAccountDeletion({})).deletion),
    retryDeletion: async () => toDeletion((await write(() => auth.retryAccountDeletion({}))).deletion),
  };
}

export type AccountApi = ReturnType<typeof createAccountApi>;
