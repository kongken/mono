import { test, expect, type Page } from "@playwright/test";

/**
 * Real-stack smoke for BFF Cookie auth (#30). Requires a running BFF + tripmap
 * (e.g. `make dev`) and `E2E_REAL_BFF=1`; see docs/bff.md §验证.
 * Not run in default CI (Playwright uses the fake backend elsewhere).
 */
const enabled = process.env.E2E_REAL_BFF === "1";

const LOCAL_KEY = "shanhai-travel-v2";
const SESSION_KEY = "shanhai-session-v1";
const localSeed = {
  version: 2,
  visits: [
    {
      code: "330100",
      name: "杭州市",
      province: "330000",
      color: "#4b937d",
      date: "2026-01-01",
      note: "local-only",
    },
  ],
  trips: [],
  marks: {},
};

async function registerUser(page: Page, username: string, password: string) {
  await page.goto("/");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await page.getByRole("tab", { name: "注册" }).click();
  await page.getByLabel("用户名", { exact: true }).fill(username);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "注册并登录" }).click();
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
}

test.describe("real BFF cookie auth", () => {
  test.skip(!enabled, "set E2E_REAL_BFF=1 with BFF+tripmap up (make dev)");

  test("desktop: login/logout, HttpOnly cookie, no token in storage", async ({
    page,
    context,
  }) => {
    const user = `pw_${Date.now().toString(36)}`;
    await page.addInitScript(
      ([key, raw]) => localStorage.setItem(key, raw),
      [LOCAL_KEY, JSON.stringify(localSeed)],
    );
    const otherCache = JSON.stringify({
      schema: 2,
      saved: { visits: [], trips: [], marks: {} },
      revision: 0,
    });
    await page.addInitScript(
      ([k, v]) => localStorage.setItem(k, v),
      ["shanhai-cloud-v2:someone-else", otherCache],
    );

    await registerUser(page, user, "password1");

    const cookies = await context.cookies();
    const sid = cookies.find((c) => c.name === "shanhai_sid");
    expect(sid?.httpOnly).toBe(true);
    expect(sid?.sameSite).toBe("Lax");
    const csrf = cookies.find((c) => c.name === "shanhai_csrf");
    expect(csrf?.httpOnly).toBeFalsy();

    const sessionRaw = await page.evaluate((k) => localStorage.getItem(k), SESSION_KEY);
    expect(sessionRaw).toBeTruthy();
    const session = JSON.parse(sessionRaw!) as { token?: string; user?: { id: string } };
    expect(session.token).toBeUndefined();

    const localAfterLogin = await page.evaluate((k) => localStorage.getItem(k), LOCAL_KEY);
    expect(localAfterLogin).toBe(JSON.stringify(localSeed));

    await page.getByRole("button", { name: "账号", exact: true }).click();
    await page.getByRole("button", { name: "退出登录", exact: true }).click();
    await page.getByRole("button", { name: "确认退出登录", exact: true }).click();
    await expect(page.getByRole("button", { name: "登录", exact: true })).toBeVisible();

    expect(await page.evaluate((k) => localStorage.getItem(k), SESSION_KEY)).toBeNull();
    expect(await page.evaluate((k) => localStorage.getItem(k), LOCAL_KEY)).toBe(
      JSON.stringify(localSeed),
    );
    expect(
      await page.evaluate((k) => localStorage.getItem(k), "shanhai-cloud-v2:someone-else"),
    ).toBe(otherCache);
  });

  test("mobile viewport: register and session cookie", async ({ page, context }) => {
    const user = `mob_${Date.now().toString(36)}`;
    await registerUser(page, user, "password1");
    const sid = (await context.cookies()).find((c) => c.name === "shanhai_sid");
    expect(sid?.httpOnly).toBe(true);
  });
});
