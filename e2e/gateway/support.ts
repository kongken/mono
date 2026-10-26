import { expect, request, test, type Page } from "@playwright/test";

/** What internal/e2e TestGatewayDomainSets tells the run about the stack it started. */
export type Account = { email: string; displayName: string };
export type Stack = {
  set: string;
  site: string;
  id: string;
  api: string;
  trip: string;
  metro: string;
  admin: string;
  mailbox: string;
  password: string;
  accounts: Record<string, Account>;
  landingHeaders: Record<string, string>;
  stateOut: string;
  stateIn: string;
};
export const stack: Stack = JSON.parse(process.env.GATEWAY_STACK ?? "{}");

/** The account of this kind for the running project (accounts are never shared between projects). */
export function acct(kind: string): Account {
  const project = test.info().project.name.replace(/^(inside|outside)-/, "");
  const found = stack.accounts[`gw-${kind}-${project}`];
  if (!found) throw new Error(`no account gw-${kind}-${project}`);
  return found;
}

export const loginAt = (app: string, returnPath: string) => `${stack.id}/login?app_id=${app}&return_path=${encodeURIComponent(returnPath)}`;

// OzID and the console are served with their images' Content-Security-Policy; a violation is a failure.
const violations = new WeakMap<Page, string[]>();
export function failOnPolicyViolations() {
  test.beforeEach(({ context, page }) => {
    const seen: string[] = [];
    violations.set(page, seen);
    context.on("console", (m) => {
      if (/Content Security Policy|Refused to (load|connect|execute|apply)/i.test(m.text())) seen.push(m.text());
    });
  });
  test.afterEach(({ page }) => {
    expect(violations.get(page)).toEqual([]);
  });
}

export const panel = (page: Page) => page.getByTestId("auth-panel");
export async function submitLogin(page: Page, who: Account) {
  await panel(page).getByLabel("邮箱").fill(who.email);
  await panel(page).getByLabel("密码", { exact: true }).fill(stack.password);
  await panel(page).getByRole("button", { name: "登录", exact: true }).click();
}

export const noOverflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);

let counter = 0;
export const uniqueEmail = (label: string) => `${label}-${Date.now().toString(36)}-${++counter}@example.com`;

/** The link of the nth mail to an address, as the scenario's mail window reports it (the vendor is the only stand-in). */
export async function mailedLink(to: string, count = 1): Promise<string> {
  const client = await request.newContext();
  try {
    const res = await client.get(`${stack.mailbox}/?to=${encodeURIComponent(to)}&count=${count}`);
    expect(res.status(), await res.text()).toBe(200);
    return ((await res.json()) as { link: string }).link;
  } finally {
    await client.dispose();
  }
}

export const tokenOf = (link: string) => new URLSearchParams(new URL(link).hash.slice(1)).get("token")!;
