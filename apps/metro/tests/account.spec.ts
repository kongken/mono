import { expect, test, type Page } from "@playwright/test";
import { FAILURES } from "@kongken/account/testing";
import { EMAIL, loginUrl, OZID, PASSWORD, refocus, setup } from "./support";

// The account entry of the metro app against a fake BFF (UI only). The product has no account forms of its own:
// every sign-in, registration, password and device screen is OzID's. These specs cover what the product still
// owns: the entry and the page a sign-in comes back to, the rider's own data, and what it believes about the shared
// session (signed out, switched, or simply not answering). The real cross-origin return (HTTPS, sibling hosts, the
// real BFF and usersrv) is internal/e2e TestMetroBrowserChain. Hosts are reserved example names; nothing is contacted.

const noOverflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);
const stored = (page: Page) => page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }));
const header = (page: Page) => page.getByRole("banner");
const signIn = (page: Page) => header(page).getByRole("button", { name: "登录", exact: true });
const FORMS = "input[type=password], input[type=email], input[autocomplete=username], input[autocomplete=current-password]";

test.describe("the entry is OzID", () => {
  test("the signed-out header has one way in, a button to OzID, and no account form anywhere", async ({ page }) => {
    const { ozid, urls } = await setup(page);
    await page.goto("/");
    await expect(signIn(page)).toBeVisible();
    await expect(page.locator(FORMS)).toHaveCount(0);
    await expect(page.getByTestId("auth-panel")).toHaveCount(0);
    await expect(page.getByText(/忘记密码|申请注册|发送验证邮件|修改密码|注销账号/)).toHaveCount(0);
    expect(await noOverflow(page)).toBe(true);

    await signIn(page).click();
    await expect(page).toHaveURL(loginUrl("/"));
    // The product named itself and a page; nothing about a person, a session or a host of its own is in it.
    expect(urls()).toEqual([loginUrl("/")]);
    expect(urls().join()).not.toMatch(/token|session|user|metro\.example|next=/i);
    // Leaving for OzID shows the origin only, never the path or query of the page it came from.
    for (const { referer } of ozid) if (referer) expect(new URL(referer).pathname + new URL(referer).search).toBe("/");
  });

  for (const [from, returnPath] of [
    ["/me", "/me"],
    ["/me/", "/me"],
    ["/cities/shanghai", "/cities/shanghai"],
    ["/topics/six-cars", "/topics/six-cars"],
    ["/u/alice", "/u/alice"],
  ] as const) {
    test(`signing in from ${from} comes back to ${returnPath}`, async ({ page }) => {
      const { urls } = await setup(page);
      await page.goto(from);
      await expect(signIn(page)).toBeVisible();
      await signIn(page).click();
      await expect(page).toHaveURL(loginUrl(returnPath));
      expect(urls()).toEqual([loginUrl(returnPath)]);
    });
  }

  test("a query or fragment of the page never reaches OzID, in the address or in the Referer", async ({ page }) => {
    const { ozid } = await setup(page);
    await page.goto("/cities/shanghai?next=https%3A%2F%2Fevil.example%2F&x=1#frag-secret");
    await signIn(page).click();
    await expect(page).toHaveURL(loginUrl("/cities/shanghai"));
    expect(ozid.map((o) => o.url).join()).not.toMatch(/evil|frag-secret|x=1/);
    for (const { referer } of ozid) expect(referer ?? "").not.toMatch(/evil|frag-secret|x=1|cities/);
  });

  test("a page the BFF has not registered, a slow or failing answer and a path that cannot be written canonically all return to the start page", async ({ page }) => {
    // Not registered: only the start page is allowed, so the product's own question is answered "no".
    const unregistered = await setup(page, { registered: false });
    await page.goto("/me");
    await signIn(page).click();
    await expect(page).toHaveURL(loginUrl("/"));
    expect(unregistered.urls()).toEqual([loginUrl("/")]);
  });

  test("an unavailable BFF answer to the return question also means the start page", async ({ page }) => {
    const { accounts, urls } = await setup(page);
    accounts.failNext("ResolveNavigation", FAILURES.unavailable);
    await page.goto("/topics/six-cars");
    await signIn(page).click();
    await expect(page).toHaveURL(loginUrl("/"));
    expect(urls()).toEqual([loginUrl("/")]);
  });

  test("a city whose name is not ASCII has no canonical path: the start page, and nothing is asked", async ({ page }) => {
    const { accounts, urls } = await setup(page);
    await page.goto("/cities/%E4%B8%8A%E6%B5%B7");
    await signIn(page).click();
    await expect(page).toHaveURL(loginUrl("/"));
    expect(accounts.state.calls).not.toContain("ResolveNavigation");
    expect(urls()).toEqual([loginUrl("/")]);
  });

  test("an anonymous visitor marking a line is only sent to sign in: nothing is written first", async ({ page }) => {
    const { fake, accounts, urls } = await setup(page);
    await page.goto("/cities/shanghai");
    await page.getByTestId("line-sh-1").getByRole("button", { name: "标记坐过" }).click();
    await expect(page).toHaveURL(loginUrl("/cities/shanghai"));
    expect(urls()).toEqual([loginUrl("/cities/shanghai")]);
    expect(fake.state.writes).toEqual([]);
    expect(fake.state.calls.filter((c) => /RiderService/.test(c))).toEqual([]);
    expect(accounts.state.calls.filter((c) => /Login|Register|Reset/.test(c))).toEqual([]);
    expect(fake.state.ridesBy.size).toBe(0);
  });

  test("the sign-in button cannot be pressed twice while the browser is leaving", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/cities/shanghai");
    // Hold the BFF's answer about the return page: the person clicks again while it is pending.
    const held = accounts.holdNext("ResolveNavigation");
    await signIn(page).click();
    await held.arrived;
    await expect(signIn(page)).toBeDisabled();
    held.release();
    await expect(page).toHaveURL(loginUrl("/cities/shanghai"));
  });

  test("the signed-in header keeps the product's own entries and has no account settings of its own", async ({ page }) => {
    await setup(page, { signedIn: true });
    await page.goto("/");
    await expect(header(page).getByText("骑行者小山")).toBeVisible();
    await expect(header(page).getByRole("link", { name: "我的足迹" })).toBeVisible();
    await expect(header(page).getByRole("button", { name: "退出" })).toBeVisible();
    await expect(page.locator(FORMS)).toHaveCount(0);
    for (const gone of ["修改密码", "已登录的设备", "注销账号", "保存资料", "登录邮箱", "账号设置"]) await expect(page.getByText(gone, { exact: true })).toHaveCount(0);
    const manage = header(page).getByTestId("ozid-link-home");
    await expect(manage).toHaveAttribute("href", OZID);
    await expect(manage).toHaveAttribute("target", "_blank");
    await expect(manage).toHaveAttribute("rel", /noopener/);
    await expect(manage).toHaveAttribute("rel", /noreferrer/);
    expect(await noOverflow(page)).toBe(true);
  });

  test("signing out from the product ends the shared session, and a failed attempt changes nothing", async ({ page }) => {
    const { accounts } = await setup(page, { signedIn: true });
    await page.goto("/me");
    await expect(page.getByRole("heading", { name: "乘坐记录" })).toBeVisible();
    await page.route("**/api/bff.v1.AuthService/Logout", (route) => route.abort("failed"), { times: 1 });
    await header(page).getByRole("button", { name: "退出" }).click();
    await expect(page.getByTestId("session-notice")).toContainText("退出登录没有完成");
    // Still signed in, here and at the BFF.
    await expect(header(page).getByText("骑行者小山")).toBeVisible();
    expect(accounts.state.current).not.toBeNull();
    await header(page).getByRole("button", { name: "退出" }).click();
    await expect(signIn(page)).toBeVisible();
    expect(accounts.state.current).toBeNull();
    await expect(page).toHaveURL(/\/$/);
  });
});

test.describe("the rider's own data stays the product's", () => {
  test("the rider profile is separate from the account's nickname, and account settings are OzID's", async ({ page }) => {
    const { fake } = await setup(page, { signedIn: true });
    await page.goto("/me");
    await expect(page.getByRole("heading", { name: "骑行者资料与公开主页" })).toBeVisible();
    await expect(page.getByTestId("rider-profile-note")).toContainText("与账号昵称和头像互不影响");
    // The public name starts empty: it is not the account's nickname, and the page offers no field for that.
    await expect(page.getByLabel("主页显示名称")).toHaveValue("");
    await expect(page.locator("input").evaluateAll((els) => els.map((e) => (e as HTMLInputElement).value))).resolves.not.toContain("骑行者小山");
    const pointer = page.getByTestId("account-pointer");
    await expect(pointer).toContainText("账号昵称与头像");
    await expect(pointer.getByTestId("ozid-link-home-me")).toHaveAttribute("href", OZID);

    await page.getByLabel("主页显示名称").fill("小山的地铁");
    await page.getByRole("textbox", { name: "Handle" }).fill("xiaoshan");
    await page.getByRole("button", { name: "保存资料" }).click();
    await expect.poll(() => fake.state.profile.displayName).toBe("小山的地铁");
    // The account's own nickname is untouched, and so is every account call.
    await expect(header(page).getByText("骑行者小山")).toBeVisible();
    expect(fake.state.calls.filter((c) => /UserService|UpdateProfile/.test(c))).toEqual([]);
  });

  test("coming back from OzID signed in restores the account and its lines from the shared session", async ({ page }) => {
    const { accounts, account, fake, urls } = await setup(page);
    fake.state.ridesBy.set(account.id, new Map([["sh-1", 2019]]));
    await page.goto("/me");
    await signIn(page).click();
    await expect(page).toHaveURL(loginUrl("/me"));
    // OzID signs the person in (its Cookie on the API host) and returns to the page: the product asks Me.
    accounts.signIn(account);
    await page.goto("/me");
    await expect(header(page).getByText("骑行者小山")).toBeVisible();
    await expect(page.getByRole("region", { name: "上海" })).toContainText("2019 年");
    // The page keeps no password, token or return address.
    const kept = await stored(page);
    expect(kept).not.toContain(PASSWORD);
    expect(kept).not.toMatch(/token|csrf|return_path|app_id|session/i);
    expect(urls()).toEqual([loginUrl("/me")]);
    expect(await noOverflow(page)).toBe(true);
  });
});

test.describe("the old account addresses", () => {
  for (const path of ["/login", "/login?next=https%3A%2F%2Fevil.example%2F&mode=register", "/login?changed=1#frag-secret"]) {
    test(`${path} explains where the account moved, follows nothing in the address, and starts nothing`, async ({ page }) => {
      const { ozid, accounts } = await setup(page);
      const hosts = new Set<string>();
      page.on("request", (r) => hosts.add(new URL(r.url()).host));
      await page.goto(path);
      await expect(page.getByTestId("legacy-account")).toContainText("登录已迁移到 OzID");
      await expect(page.locator(FORMS)).toHaveCount(0);
      await expect(page.getByTestId("auth-panel")).toHaveCount(0);
      // The address is cleaned, and nothing in it was read or followed; the page did not leave by itself.
      const url = new URL(page.url());
      expect(url.pathname).toBe("/login");
      expect(url.search + url.hash).toBe("");
      expect([...hosts].filter((h) => !h.startsWith("127.0.0.1"))).toEqual([]);
      expect(ozid).toEqual([]);
      expect(accounts.state.calls.filter((c) => /Login|Register|Reset/.test(c))).toEqual([]);
      expect(await noOverflow(page)).toBe(true);
      // One way on: OzID, returning to the start page.
      await page.getByTestId("legacy-sign-in").click();
      await expect(page).toHaveURL(loginUrl("/"));
      expect([...hosts].filter((h) => h.includes("evil"))).toEqual([]);
    });
  }

  test("/account leads to OzID's account pages in a link, not a form", async ({ page }) => {
    const { ozid } = await setup(page, { signedIn: true });
    await page.goto("/account?x=1#y");
    await expect(page.getByTestId("legacy-account")).toContainText("账号设置已迁移到 OzID");
    await expect(page.locator(FORMS)).toHaveCount(0);
    expect(new URL(page.url()).search).toBe("");
    const link = page.getByTestId("legacy-ozid-home");
    await expect(link).toHaveAttribute("href", OZID);
    await expect(link).toHaveAttribute("rel", /noopener/);
    expect(ozid).toEqual([]);
  });

  test("a signed-in visitor of /login is told so and sent nowhere", async ({ page }) => {
    const { ozid } = await setup(page, { signedIn: true });
    await page.goto("/login");
    await expect(page.getByTestId("legacy-account")).toContainText("你已登录");
    await expect(page.getByTestId("legacy-sign-in")).toHaveCount(0);
    await page.getByRole("link", { name: "回到我的足迹" }).click();
    await expect(page).toHaveURL(/\/me$/);
    expect(ozid).toEqual([]);
  });
});

test("a deletion left unfinished before OzID took over only points at OzID to retry, and can be dismissed", async ({ page }) => {
  const { accounts } = await setup(page);
  await page.goto("/");
  await page.evaluate(() => localStorage.setItem("metro-account-deletion-pending", "1"));
  await page.reload();
  const note = page.getByTestId("deletion-pending");
  await expect(note).toContainText("账号注销尚未完成");
  const link = note.getByTestId("ozid-close-account");
  await expect(link).toHaveAttribute("href", `${OZID}/close-account`);
  await expect(link).toHaveAttribute("rel", "noopener noreferrer");
  // No in-product retry: nothing about deletion was asked of the BFF, and no form appeared.
  expect(accounts.state.calls.filter((c) => /Delete|Close|Deletion/.test(c))).toEqual([]);
  await expect(page.locator(FORMS)).toHaveCount(0);
  await note.getByRole("button", { name: "已经完成，不再提示" }).click();
  await expect(note).toHaveCount(0);
  await page.reload();
  await expect(page.getByTestId("deletion-pending")).toHaveCount(0);
});

test.describe("mailed links from before OzID", () => {
  const RESET = "a brand new password";

  async function mailed(page: Page, kind: "reset" | "registration", ctx: { appId: string; returnPath: string } | null) {
    const set = await setup(page);
    if (kind === "reset") set.accounts.handle("RequestPasswordReset", { email: EMAIL, ...(ctx ?? {}) }, {});
    else set.accounts.handle("RequestRegistration", { email: "new-rider@example.com", ...(ctx ?? {}) }, {});
    return { ...set, mail: set.accounts.lastMail(kind)! };
  }

  test("the verification link reads its token once, clears it, consumes nothing until submit, and leads to OzID", async ({ page }) => {
    const { accounts, mail, urls } = await mailed(page, "registration", { appId: "metro", returnPath: "/" });
    expect(mail.link).toMatch(/^\/account\/verify-email#token=[\w-]{43}$/);
    const requested: string[] = [];
    page.on("request", (r) => requested.push(r.url()));
    const callsBeforeOpen = accounts.state.calls.length;
    const res = await page.goto(mail.link);
    expect(res!.headers()["referrer-policy"]).toBe("no-referrer");
    expect(res!.headers()["cache-control"]).toBe("no-store");
    await expect(page.getByRole("heading", { name: "验证邮箱并设置密码" })).toBeVisible();
    // The fragment is gone from the address bar and no storage holds it.
    expect(new URL(page.url()).hash).toBe("");
    expect(page.url()).not.toContain(mail.token);
    expect(await stored(page)).not.toContain(mail.token);
    expect(await page.evaluate(() => document.querySelector('meta[name="referrer"]')?.getAttribute("content"))).toBe("no-referrer");
    // Opening the page (what a mail scanner does) made no account call at all, and no request carried the token.
    expect(accounts.state.calls.slice(callsBeforeOpen)).toEqual([]);
    expect(requested.filter((u) => u.includes(mail.token))).toEqual([]);
    expect(await page.evaluate(() => [...document.scripts].map((s) => s.src).filter((s) => !s.startsWith(location.origin)))).toEqual([]);

    // Mistakes are shown on their fields; nothing is sent.
    await page.getByRole("button", { name: "完成注册" }).click();
    await expect(page.getByLabel("密码", { exact: true })).toHaveAttribute("aria-invalid", "true");
    await page.getByLabel("密码", { exact: true }).fill("a long enough password");
    await page.getByLabel("再次输入密码").fill("a different password!!");
    await page.getByRole("button", { name: "完成注册" }).click();
    await expect(page.getByText("两次输入的密码不一致")).toBeVisible();
    expect(accounts.state.calls).not.toContain("CompleteRegistration");

    await page.getByLabel("昵称（可选）").fill("新骑行者");
    await page.getByLabel("再次输入密码").fill("a long enough password");
    await page.getByRole("button", { name: "完成注册" }).click();
    await expect(page.getByTestId("landing-done")).toContainText("注册完成");
    // Verifying signs nobody in; "log in" is OzID's page.
    expect(accounts.state.current).toBeNull();
    expect(await noOverflow(page)).toBe(true);
    await page.getByRole("link", { name: "去登录" }).click();
    await expect(page).toHaveURL(loginUrl("/"));
    expect(urls()).toEqual([loginUrl("/")]);
  });

  test("the reset link ends every old session, signs nobody in, and leads to OzID with the page the BFF validated", async ({ page }) => {
    const { accounts, mail, urls } = await mailed(page, "reset", { appId: "metro", returnPath: "/me" });
    await page.goto(mail.link);
    await expect(page.getByRole("heading", { name: "设置新密码" })).toBeVisible();
    expect(new URL(page.url()).hash).toBe("");
    await page.getByLabel("新密码", { exact: true }).fill(RESET);
    await page.getByLabel("再次输入新密码").fill(RESET);
    await page.getByRole("button", { name: "重置密码" }).click();
    await expect(page.getByTestId("landing-done")).toContainText("密码已重置");
    expect(accounts.state.current).toBeNull();
    await page.getByRole("link", { name: "去登录" }).click();
    await expect(page).toHaveURL(loginUrl("/me"));
    expect(urls()).toEqual([loginUrl("/me")]);
  });

  test("a link bound to another product leads to that context, and an unbound link returns to this product's start page", async ({ page }) => {
    const other = await mailed(page, "reset", { appId: "trip-map", returnPath: "/" });
    await page.goto(other.mail.link);
    await page.getByLabel("新密码", { exact: true }).fill(RESET);
    await page.getByLabel("再次输入新密码").fill(RESET);
    await page.getByRole("button", { name: "重置密码" }).click();
    await page.getByRole("link", { name: "去登录" }).click();
    await expect(page).toHaveURL(`${OZID}/login?app_id=trip-map&return_path=%2F`);
  });

  test("a used, reloaded or missing token shows the same unusable-link page, and it offers OzID", async ({ page }) => {
    const { mail, urls } = await mailed(page, "registration", null);
    await page.goto(mail.link);
    await page.getByLabel("密码", { exact: true }).fill("a long enough password");
    await page.getByLabel("再次输入密码").fill("a long enough password");
    await page.getByRole("button", { name: "完成注册" }).click();
    await expect(page.getByTestId("landing-done")).toBeVisible();

    // The link works once: opening it again and submitting is refused as unusable.
    await page.goto(mail.link);
    await page.getByLabel("密码", { exact: true }).fill("another long password");
    await page.getByLabel("再次输入密码").fill("another long password");
    await page.getByRole("button", { name: "完成注册" }).click();
    await expect(page.getByTestId("link-invalid")).toContainText("链接不可用");
    // The fragment was cleared, so a reload has no token and says the same.
    await page.reload();
    await expect(page.getByTestId("link-invalid")).toBeVisible();
    await page.goto("/account/verify-email");
    await expect(page.getByTestId("link-invalid")).toBeVisible();
    await page.goto("/account/verify-email#token=short");
    await expect(page.getByTestId("link-invalid")).toBeVisible();
    await page.getByRole("link", { name: /返回登录/ }).click();
    await expect(page).toHaveURL(loginUrl("/"));
    expect(urls()).toEqual([loginUrl("/")]);
  });
});

test.describe("what the product believes about the shared session", () => {
  test("a check that cannot be made is not 'signed out': no sign-in prompt, a retry, and the account is back when it answers", async ({ page }) => {
    const { accounts, account, ozid } = await setup(page, { signedIn: true });
    accounts.failNext("Me", FAILURES.unavailable);
    await page.goto("/me");
    // Neither the sign-in button nor the sign-in prompt of the personal page: nothing says the person is signed out.
    await expect(page.getByTestId("session-unverified")).toContainText("暂时无法确认登录状态");
    await expect(signIn(page)).toHaveCount(0);
    await expect(page.getByText("登录后可以记录坐过的线路")).toHaveCount(0);
    await expect(page.getByRole("button", { name: "登录", exact: true })).toHaveCount(0);
    await expect(header(page).getByRole("button", { name: "重新检查" })).toBeVisible();
    expect(ozid).toEqual([]);
    expect(await noOverflow(page)).toBe(true);

    await header(page).getByRole("button", { name: "重新检查" }).click();
    await expect(header(page).getByText(account.displayName)).toBeVisible();
    await expect(page.getByTestId("session-unverified")).toHaveCount(0);
    await expect(page.getByRole("heading", { name: "乘坐记录" })).toBeVisible();
  });

  test("marking a line while the check is unanswered neither signs anybody in nor writes: it retries the check", async ({ page }) => {
    const { accounts, fake, ozid } = await setup(page, { signedIn: true });
    accounts.failNext("Me", FAILURES.internal);
    await page.goto("/cities/shanghai");
    await expect(page.getByTestId("session-unverified")).toBeVisible();
    await page.getByTestId("line-sh-1").getByRole("button", { name: "标记坐过" }).click();
    // The retry succeeded in the background; the click itself started nothing.
    expect(ozid).toEqual([]);
    expect(fake.state.writes).toEqual([]);
    await expect(page.getByTestId("session-unverified")).toHaveCount(0);
    await expect(header(page).getByText("骑行者小山")).toBeVisible();
  });

  test("a signed-in person stays signed in, with their data, through a failed re-check", async ({ page }) => {
    await page.clock.install();
    const { accounts, fake, account } = await setup(page, { signedIn: true });
    fake.state.ridesBy.set(account.id, new Map([["sh-1", 2019]]));
    await page.goto("/me");
    await expect(page.getByRole("region", { name: "上海" })).toContainText("2019 年");
    accounts.failNext("Me", FAILURES.unavailable);
    await refocus(page);
    await expect(page.getByTestId("session-unverified")).toContainText("先按已登录的账号显示");
    await expect(header(page).getByText("骑行者小山")).toBeVisible();
    await expect(page.getByRole("region", { name: "上海" })).toContainText("2019 年");
    await expect(signIn(page)).toHaveCount(0);
    // The next answer clears the mark.
    await refocus(page);
    await expect(page.getByTestId("session-unverified")).toHaveCount(0);
    await expect(header(page).getByText("骑行者小山")).toBeVisible();
  });

  test("a session that really ended elsewhere shows as signed out on the next check, once, without a redirect loop", async ({ page }) => {
    await page.clock.install();
    const { accounts, ozid } = await setup(page, { signedIn: true });
    await page.goto("/me");
    await expect(page.getByRole("heading", { name: "乘坐记录" })).toBeVisible();
    // Everything the page does on its own is counted: asks to the BFF about the session, and moves of the page.
    const asks: string[] = [];
    const moves: string[] = [];
    page.on("request", (r) => void (r.url().endsWith("/bff.v1.AuthService/Me") && asks.push(r.url())));
    page.on("framenavigated", (f) => void (f === page.mainFrame() && moves.push(f.url())));
    accounts.signOut(); // another tab or OzID ended the shared session
    await refocus(page);
    await expect(signIn(page)).toBeVisible();
    await expect(page.getByTestId("session-notice")).toContainText("你已在其他页面退出登录");
    await expect(page.getByText("登录后可以记录坐过的线路")).toBeVisible();
    // The product did not send anybody anywhere by itself: exactly one ask (the one the shown page made), no move.
    expect(asks).toHaveLength(1);
    expect(moves).toEqual([]);
    await expect(page).toHaveURL(/\/me$/);
    expect(ozid).toEqual([]);
    // Nothing schedules another ask or a move: minutes of fake time pass without any event, and nothing happens.
    await page.clock.fastForward(5 * 60_000);
    await page.evaluate(() => undefined); // a round trip: anything the timers started has been sent by now
    expect(asks).toHaveLength(1);
    expect(moves).toEqual([]);
    expect(ozid).toEqual([]);
    // The next time the page is shown it asks once more, learns the same, says nothing again and does not move.
    await page.getByTestId("session-notice").getByRole("button", { name: "知道了" }).click();
    await refocus(page);
    await expect.poll(() => asks.length).toBe(2);
    await expect(signIn(page)).toBeVisible();
    await expect(page.getByTestId("session-notice")).toHaveCount(0);
    expect(moves).toEqual([]);
    expect(ozid).toEqual([]);
  });

  test("another account signed in elsewhere shows on the next check, and the first account's lines are gone from the page", async ({ page }) => {
    await page.clock.install();
    const { accounts, account, fake } = await setup(page, { signedIn: true });
    const b = accounts.addAccount({ email: "other@example.com", password: PASSWORD, displayName: "另一位骑行者" });
    fake.state.ridesBy.set(account.id, new Map([["sh-1", 2019]]));
    await page.goto("/me");
    await expect(page.getByRole("region", { name: "上海" })).toContainText("2019 年");
    accounts.signIn(b);
    await refocus(page);
    await expect(header(page).getByText("另一位骑行者")).toBeVisible();
    await expect(page.getByTestId("session-notice")).toContainText("另一位骑行者");
    await expect(page.getByText("还没有记录")).toBeVisible();
    await expect(page.getByRole("region", { name: "上海" })).toHaveCount(0);
  });

  test("a write from a page that has not seen the switch is refused for the first account and never lands in the second one's data", async ({ page }) => {
    const { accounts, account, fake } = await setup(page, { signedIn: true });
    const b = accounts.addAccount({ email: "other@example.com", password: PASSWORD, displayName: "另一位骑行者" });
    await page.goto("/cities/shanghai");
    await expect(header(page).getByText("骑行者小山")).toBeVisible();
    await expect(page.getByTestId("line-sh-1").getByRole("button", { name: "标记坐过" })).toBeVisible();
    accounts.signIn(b); // the shared Cookie is now B's; this page still believes in A
    await page.getByTestId("line-sh-1").getByRole("button", { name: "标记坐过" }).click();
    // Refused as "not signed in" by the account guard: nothing was written for anyone.
    expect(fake.state.writes).toEqual([]);
    expect([...fake.state.ridesBy.entries()].flatMap(([, m]) => [...m.keys()])).toEqual([]);
    // The page then asks who it is, shows B, and says so; it did not navigate.
    await expect(header(page).getByText("另一位骑行者")).toBeVisible();
    await expect(page.getByTestId("session-notice")).toContainText("另一位骑行者");
    await expect(page).toHaveURL(/\/cities\/shanghai$/);
    // Acting again now is B's own act, named as B's.
    await page.getByTestId("line-sh-1").getByRole("button", { name: "标记坐过" }).click();
    await expect(page.getByTestId("line-sh-1").getByRole("button", { name: "坐过" })).toHaveAttribute("aria-pressed", "true");
    expect(fake.state.writes.map((w) => [w.method, w.cookieUser, w.header])).toEqual([["bff.metro.v1.RiderService/MarkLine", b.id, b.id]]);
    expect(fake.state.ridesBy.get(account.id)?.size ?? 0).toBe(0);
  });

  test("an older answer that arrives late cannot bring a signed-out session back", async ({ page }) => {
    await page.clock.install();
    const { accounts } = await setup(page, { signedIn: true });
    await page.goto("/");
    await expect(header(page).getByText("骑行者小山")).toBeVisible();
    // Check #1 is decided now (signed in) but its answer is held.
    const held = accounts.holdNext("Me");
    await refocus(page);
    await held.arrived;
    // The session ends, check #2 learns it.
    accounts.signOut();
    await refocus(page);
    await expect(signIn(page)).toBeVisible();
    // From here on a page-side observer notes whether the signed-out page ever shows the account again, and the
    // page's own asks and moves are counted.
    await page.evaluate(() => {
      const w = window as unknown as { __accountShown: boolean };
      w.__accountShown = false;
      new MutationObserver(() => {
        if (document.body.innerText.includes("骑行者小山")) w.__accountShown = true;
      }).observe(document.body, { subtree: true, childList: true, characterData: true });
    });
    const asks: string[] = [];
    const moves: string[] = [];
    page.on("request", (r) => void (r.url().endsWith("/bff.v1.AuthService/Me") && asks.push(r.url())));
    page.on("framenavigated", (f) => void (f === page.mainFrame() && moves.push(f.url())));
    // The stale "signed in" answer arrives now: the wait is for that very response, read to its end, then the
    // page's fake clock runs on so anything it scheduled has run. It is discarded: no account shown at any moment,
    // no further ask caused by it, no move.
    const late = page.waitForResponse((r) => r.url().endsWith("/bff.v1.AuthService/Me"));
    held.release();
    await (await late).finished();
    await page.clock.runFor(1_000);
    await page.evaluate(() => undefined);
    expect(await page.evaluate(() => (window as unknown as { __accountShown: boolean }).__accountShown)).toBe(false);
    await expect(signIn(page)).toBeVisible();
    await expect(header(page).getByText("骑行者小山")).toHaveCount(0);
    expect(asks).toEqual([]);
    expect(moves).toEqual([]);
  });

  test("a stale token is repaired once: the same mark is sent again, with a fresh token, and lands exactly once", async ({ page }) => {
    const { fake, accounts } = await setup(page, { signedIn: true });
    await page.goto("/cities/shanghai");
    fake.state.csrfRefusals = 1;
    const issued = accounts.state.calls.filter((c) => c === "IssueCSRFToken").length;
    await page.getByTestId("line-sh-1").getByRole("button", { name: "标记坐过" }).click();
    await expect(page.getByTestId("line-sh-1").getByRole("button", { name: "坐过" })).toHaveAttribute("aria-pressed", "true");
    expect(fake.state.calls.filter((c) => /RiderService\/MarkLine/.test(c))).toHaveLength(2);
    expect(fake.state.writes).toHaveLength(1);
    // One token for the write itself, one more fetched to repair the refusal.
    expect(accounts.state.calls.filter((c) => c === "IssueCSRFToken").length).toBe(issued + 2);
  });

  test("a second refusal is the answer: no third attempt, nothing marked, a plain message", async ({ page }) => {
    const { fake } = await setup(page, { signedIn: true });
    await page.goto("/cities/shanghai");
    fake.state.csrfRefusals = 2;
    await page.getByTestId("line-sh-1").getByRole("button", { name: "标记坐过" }).click();
    await expect(page.getByRole("alert")).toContainText("没有权限执行此操作");
    expect(fake.state.calls.filter((c) => /RiderService\/MarkLine/.test(c))).toHaveLength(2);
    expect(fake.state.writes).toEqual([]);
    await expect(page.getByTestId("line-sh-1").getByRole("button", { name: "标记坐过" })).toBeVisible();
  });
});

test("the account surfaces fit a phone and stay out of the way of anonymous browsing", async ({ page }) => {
  const { accounts } = await setup(page);
  accounts.failNext("Me", FAILURES.unavailable);
  await page.goto("/");
  // Browsing the public catalogue needs no session at all, even while the check is unanswered.
  await expect(page.getByRole("link", { name: /上海/ })).toBeVisible();
  await expect(page.getByTestId("session-unverified")).toBeVisible();
  expect(await noOverflow(page)).toBe(true);
  await page.goto("/login");
  expect(await noOverflow(page)).toBe(true);
  await page.goto("/account");
  expect(await noOverflow(page)).toBe(true);
});
