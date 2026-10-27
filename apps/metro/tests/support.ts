import type { Page } from "@playwright/test";
import { createFakeAccounts, installFakeAccounts } from "@kongken/account/testing";
import { fakeBackend } from "./fake-backend";

export const OZID = "https://id.example.com";
export const EMAIL = "rider@example.com";
export const PASSWORD = "correct horse battery";
/** What the BFF's registry lists for this product: the pages a sign-in may return to (the start page is implicit). */
export const REGISTERED = ["/me", "/cities/*", "/topics", "/topics/*", "/u/*"];

/**
 * The metro app against a fake BFF: the catalogue and rider service (fake-backend.ts) and the shared account
 * half (`@kongken/account/testing`), with OzID stood in for by a recorder (nothing real is contacted). The Cookie
 * session is the account fake's `current`; the rider fake follows it.
 */
export async function setup(page: Page, opts: { signedIn?: boolean; registered?: boolean } = {}) {
  const fake = fakeBackend(page);
  const accounts = createFakeAccounts({
    navigation: { "trip-map": { origin: "https://trip.example.com" }, metro: { origin: "https://metro.example.com", ...(opts.registered === false ? {} : { paths: REGISTERED }) } },
    onSession: (a) => {
      fake.state.user = a ? { id: a.id, displayName: a.displayName, roles: a.roles } : null;
    },
  });
  await fake.install();
  await installFakeAccounts(page, accounts);
  // The OzID stand-in records what the product sent it (address and Referer); it renders nothing real.
  const ozid: { url: string; referer: string | undefined }[] = [];
  await page.route(`${OZID}/**`, (route) => {
    ozid.push({ url: route.request().url(), referer: route.request().headers()["referer"] });
    return route.fulfill({ status: 200, contentType: "text/html", body: "<title>OzID stand-in</title><p>OzID stand-in</p>" });
  });
  const account = accounts.addAccount({ email: EMAIL, password: PASSWORD, displayName: "骑行者小山" });
  if (opts.signedIn) accounts.signIn(account);
  return { fake, accounts, account, ozid, urls: () => ozid.map((o) => o.url) };
}

export const loginUrl = (returnPath: string) => `${OZID}/login?app_id=metro&return_path=${encodeURIComponent(returnPath)}`;

/** A signed-in write is always an explicit save in the ride editor. */
export async function saveRide(page: Page, code: string, year?: string) {
  await page.getByTestId(`line-${code}`).getByRole("button", { name: "标记坐过" }).click();
  const dialog = page.getByRole("dialog");
  if (year !== undefined) await dialog.getByLabel("乘坐年份（可选）").fill(year);
  await dialog.getByRole("button", { name: "保存记录", exact: true }).click();
}

export async function removeRide(page: Page, code: string) {
  await page.getByTestId(`line-${code}`).getByRole("button", { name: "编辑足迹" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "移除此线路足迹" }).click();
}

/** The page asks Me again when it is shown or focused again; this is that moment (the ask is rate limited). */
export async function refocus(page: Page) {
  await page.clock.fastForward(6_000);
  await page.evaluate(() => {
    window.dispatchEvent(new Event("focus"));
    document.dispatchEvent(new Event("visibilitychange"));
  });
}
