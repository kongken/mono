import { test, expect, type Page } from "@playwright/test";

/**
 * Real-stack session smoke. Requires BFF + tripmap and a Playwright storage
 * state captured after a real Logto login. Keep that file outside the repo.
 * E2E_REAL_BFF=1 E2E_LOGTO_STORAGE_STATE=/private/path/state.json; see docs/bff.md.
 * Not run in default CI (Playwright uses the fake backend elsewhere).
 */
const enabled = process.env.E2E_REAL_BFF === "1" && !!process.env.E2E_LOGTO_STORAGE_STATE;
test.use({ storageState: process.env.E2E_LOGTO_STORAGE_STATE });

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

async function restoreLogtoSession(page: Page) {
  await page.goto("/");
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
}

test.describe("real BFF cookie auth", () => {
  test.skip(!enabled, "set E2E_REAL_BFF=1 and E2E_LOGTO_STORAGE_STATE with BFF+tripmap up");

  test("desktop: login/logout, HttpOnly cookie, no token in storage", async ({
    page,
    context,
  }) => {
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

    await restoreLogtoSession(page);

    const cookies = await context.cookies();
    const sid = cookies.find((c) => c.name === "kongken_sid");
    expect(sid?.httpOnly).toBe(true);
    expect(sid?.sameSite).toBe("Lax");
    const csrf = cookies.find((c) => c.name === "kongken_csrf");
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

  test("mobile viewport: restore Logto session cookie", async ({ page, context }) => {
    await restoreLogtoSession(page);
    const sid = (await context.cookies()).find((c) => c.name === "kongken_sid");
    expect(sid?.httpOnly).toBe(true);
  });
});
