/**
 * Client-side checks for the account forms. They mirror the rules of usersrv (spec 4.1,
 * 4.2) only to save a round trip and to point at the wrong field; the server stays the
 * authority and its answer is always honoured.
 */

export const MIN_PASSWORD_CHARS = 12;
export const MAX_PASSWORD_CHARS = 128;
export const MAX_PASSWORD_BYTES = 512;
export const MAX_EMAIL_BYTES = 254;
export const MAX_EMAIL_LOCAL_BYTES = 64;
export const MAX_DISPLAY_NAME_CHARS = 40;
export const MAX_AVATAR_URL_BYTES = 500;

const encoder = new TextEncoder();
const byteLength = (s: string) => encoder.encode(s).length;
const charCount = (s: string) => Array.from(s).length;

/** The spelling the server treats as the same mailbox: ASCII whitespace trimmed, not lowercased (the server does that). */
export const trimEmail = (input: string) => input.replace(/^[ \t\v\f]+|[ \t\v\f]+$/g, "");

const LOCAL = /^[A-Za-z0-9!#$%&'*+/=?^_`{|}~-]+(\.[A-Za-z0-9!#$%&'*+/=?^_`{|}~-]+)*$/;
const LABEL = /^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$/;

/** A Chinese message when the address cannot be a login email, otherwise null. */
export function checkEmail(input: string): string | null {
  const email = trimEmail(input);
  if (email === "") return "请输入邮箱地址";
  if (/[\r\n]/.test(email) || /[^\x21-\x7e]/.test(email)) return "邮箱只能包含英文字母、数字和常见符号，不能有空格或中文";
  if (byteLength(email) > MAX_EMAIL_BYTES) return "邮箱地址太长";
  const at = email.indexOf("@");
  if (at <= 0 || at !== email.lastIndexOf("@")) return "请输入完整的邮箱地址，例如 name@example.com";
  const local = email.slice(0, at);
  const domain = email.slice(at + 1);
  if (local.length > MAX_EMAIL_LOCAL_BYTES || !LOCAL.test(local)) return "邮箱地址格式不正确";
  const labels = domain.split(".");
  if (labels.length < 2 || !labels.every((l) => LABEL.test(l))) return "邮箱地址格式不正确";
  return null;
}

/** Password rules for choosing a password: 12-128 characters, at most 512 bytes; nothing is trimmed or normalised. */
export function checkNewPassword(password: string): string | null {
  if (password === "") return "请输入密码";
  const n = charCount(password);
  if (n < MIN_PASSWORD_CHARS) return `密码至少 ${MIN_PASSWORD_CHARS} 个字符，可以是几个词组成的短句`;
  if (n > MAX_PASSWORD_CHARS) return `密码最多 ${MAX_PASSWORD_CHARS} 个字符`;
  if (byteLength(password) > MAX_PASSWORD_BYTES) return "密码太长，请缩短后再试";
  return null;
}

/** A password that is only compared (login, re-authentication, current password): no minimum, so an older shorter one still gets to the server. */
export function checkExistingPassword(password: string): string | null {
  if (password === "") return "请输入密码";
  if (charCount(password) > MAX_PASSWORD_CHARS || byteLength(password) > MAX_PASSWORD_BYTES) return "密码太长，请检查后重试";
  return null;
}

export function checkPasswordConfirmation(password: string, confirmation: string): string | null {
  return password === confirmation ? null : "两次输入的密码不一致";
}

const hasControl = (s: string) => Array.from(s).some((c) => /[\p{Cc}\u2028\u2029]/u.test(c));

/** The nickname after trimming; empty is allowed only where the server fills in its default. */
export function checkDisplayName(input: string, allowEmpty: boolean): string | null {
  const name = input.trim();
  if (name === "") return allowEmpty ? null : "请输入昵称";
  if (hasControl(name)) return "昵称不能包含控制字符";
  if (charCount(name) > MAX_DISPLAY_NAME_CHARS) return `昵称最多 ${MAX_DISPLAY_NAME_CHARS} 个字符`;
  return null;
}

/** Empty (no avatar) or an https URL without credentials, at most 500 bytes. */
export function checkAvatarUrl(input: string): string | null {
  if (input === "") return null;
  if (byteLength(input) > MAX_AVATAR_URL_BYTES) return `头像地址最长 ${MAX_AVATAR_URL_BYTES} 个字符`;
  if (/[^\x21-\x7e]/.test(input)) return "头像地址只能是 ASCII 字符，不能有空格";
  try {
    const u = new URL(input);
    if (u.protocol !== "https:" || u.username || u.password || u.hash) return "头像需要是 https 地址，且不含账号信息";
  } catch {
    return "头像需要是 https 地址，且不含账号信息";
  }
  return null;
}

/** An idempotency key for one account deletion: 1-64 characters of [A-Za-z0-9._-]. */
export function newMutationId(random: () => string = () => globalThis.crypto.randomUUID()): string {
  return `del-${random()}`;
}
