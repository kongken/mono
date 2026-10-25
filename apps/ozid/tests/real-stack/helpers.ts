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
export const form = (page: Page) => page.getByTestId("close-form");
export const kind = (testInfo: { project: { name: string } }) => testInfo.project.name;

