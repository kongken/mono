import { expect, request, type BrowserContext, type Page } from "@playwright/test";

// Shared by the real-stack close-account specs: the addresses of the stack started by
// internal/e2e TestOzidBrowserChain, its control origin and a few page helpers.

export type Stack = { id: string; api: string; metro: string; trip: string; control: string; password: string };
export const stack: Stack = JSON.parse(process.env.OZID_REAL_STACK ?? "{}");

export type State = { mediaRows: number; object: boolean; visits: number; metroRows: number; workflow: string };

/** The control origin of the scenario: the front is local, so dial it with the control Host. */
export async function control<T = unknown>(path: string): Promise<T> {
  const c = new URL(stack.control);
  const client = await request.newContext({ ignoreHTTPSErrors: true });
  try {
    const res = await client.fetch(`https://127.0.0.1:${c.port}${path}`, { headers: { Host: c.host } });
    expect(res.status(), await res.text()).toBeLessThan(300);
    return res.status() === 204 ? (undefined as T) : ((await res.json()) as T);
  } finally {
    await client.dispose();
  }
}
export const state = (subject: string) => control<State>(`/state?subject=${subject}`);
export const failMetro = (on: boolean) => control(`/fail-metro?on=${on ? 1 : 0}`);

export async function signIn(page: Page, subject: string) {
  await page.goto(`${stack.id}/login`);
  const panel = page.getByTestId("auth-panel");
  await panel.getByLabel("邮箱").fill(`${subject}@example.com`);
  await panel.getByLabel("密码", { exact: true }).fill(stack.password);
  await panel.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByTestId("overview")).toBeVisible();
}

export const sid = async (context: BrowserContext) => (await context.cookies()).find((c) => c.name === "kongken_sid");
/**
 * The dedicated, HttpOnly Cookies that only show and continue an account deletion: one immutable Cookie per
 * recovery generation, named kongken_del_<16 hex> after its value.
 */
export const recoveries = async (context: BrowserContext) => (await context.cookies()).filter((c) => /^kongken_del_[0-9a-f]{16}$/.test(c.name));
/** The one recovery Cookie of the browser, if any (more than one fails the test: use recoveries). */
export const recovery = async (context: BrowserContext) => {
  const all = await recoveries(context);
  expect(all.length, `recovery cookies: ${all.map((c) => c.name).join(",")}`).toBeLessThanOrEqual(1);
  return all[0];
};

/** The recovery Cookie is host-only on the API host, HttpOnly, Secure and SameSite=Lax, and no page can read it. */
export async function expectRecoveryAttributes(context: BrowserContext, page: Page) {
  const cookie = await recovery(context);
  expect(cookie, "recovery cookie present").toBeTruthy();
  expect(cookie).toMatchObject({ httpOnly: true, secure: true, sameSite: "Lax", path: "/" });
  expect(cookie!.domain, "host-only: no root-domain Domain attribute").toBe(new URL(stack.api).hostname);
  // Not in anything a page can reach: its own storage and document.cookie.
  const reachable = await page.evaluate(() => JSON.stringify([document.cookie, { ...localStorage }, { ...sessionStorage }]));
  expect(reachable).not.toContain(cookie!.value);
  return cookie!;
}
export const form = (page: Page) => page.getByTestId("close-form");
export const kind = (testInfo: { project: { name: string } }) => testInfo.project.name;

