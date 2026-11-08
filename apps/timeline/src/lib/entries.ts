import type { Entry } from "@kongken/api-client/bff/timeline/v1/timeline_pb";

/**
 * The screen's own vocabulary, kept free of React and of the network so every rule here can be asserted by a
 * plain Node test: grouping the timeline, naming a day, writing an amount in its currency and validating the
 * "记一笔" draft the way the server will validate it (see internal/timeline/application/timeline.go).
 */

/** The two views the sidebar switches between; both read the same list. */
export type View = "timeline" | "trash";

/** What the search box and the filter bar send with every list call (empty means unfiltered). */
export type FilterState = {
  query: string;
  category: string;
  fromDate: string;
  toDate: string;
};

export const EMPTY_FILTERS: FilterState = { query: "", category: "", fromDate: "", toDate: "" };

export type ActiveFilter = { key: keyof FilterState; label: string };

/** The filters in effect, in the order they are shown above the timeline. */
export function activeFilters(filters: FilterState): ActiveFilter[] {
  const out: ActiveFilter[] = [];
  const query = filters.query.trim();
  if (query !== "") out.push({ key: "query", label: `搜索：${query}` });
  if (filters.category.trim() !== "") out.push({ key: "category", label: `分类：${filters.category.trim()}` });
  const range = describeRange(filters.fromDate, filters.toDate);
  if (range !== "") out.push({ key: "fromDate", label: range });
  return out;
}

function describeRange(from: string, to: string): string {
  if (from !== "" && to !== "") return `日期：${from} ~ ${to}`;
  if (from !== "") return `日期：${from} 起`;
  if (to !== "") return `日期：截至 ${to}`;
  return "";
}

/** The request fields for a list call: trimmed, and never a half-open range the server would have to guess. */
export function listRequest(filters: FilterState, includeDeleted: boolean): {
  query: string;
  category: string;
  fromDate: string;
  toDate: string;
  includeDeleted: boolean;
} {
  return {
    query: filters.query.trim(),
    category: filters.category.trim(),
    fromDate: filters.fromDate,
    toDate: filters.toDate,
    includeDeleted,
  };
}

const WEEKDAYS = ["星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"];

const DATE_RE = /^(\d{4})-(\d{2})-(\d{2})$/;
const TIME_RE = /^([01]\d|2[0-3]):([0-5]\d)(:([0-5]\d))?$/;
const AMOUNT_RE = /^\d+(\.\d+)?$/;

/** The local calendar day as `YYYY-MM-DD`, which is what the list filters and the date field take. */
export function localToday(clock: Date = new Date()): string {
  const month = String(clock.getMonth() + 1).padStart(2, "0");
  const day = String(clock.getDate()).padStart(2, "0");
  return `${clock.getFullYear()}-${month}-${day}`;
}

/** True for a date the server accepts (`YYYY-MM-DD`, a real day). */
export function isValidDate(value: string): boolean {
  const match = DATE_RE.exec(value);
  if (match === null) return false;
  const [, year, month, day] = match;
  const date = new Date(Number(year), Number(month) - 1, Number(day));
  return date.getFullYear() === Number(year) && date.getMonth() === Number(month) - 1 && date.getDate() === Number(day);
}

/** True for a time the server accepts (`HH:MM` or `HH:MM:SS`); the empty string is "no time". */
export function isValidTime(value: string): boolean {
  return value === "" || TIME_RE.test(value);
}

/** `11 月 7 日`: the day heading's own label, with no weekday. */
export function dayLabel(date: string): string {
  const match = DATE_RE.exec(date);
  if (match === null) return date;
  return `${Number(match[2])} 月 ${Number(match[3])} 日`;
}

/** `星期六`. */
export function weekdayLabel(date: string): string {
  const match = DATE_RE.exec(date);
  if (match === null) return "";
  return WEEKDAYS[new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3])).getDay()] ?? "";
}

/** `2026 年 11 月`. */
export function monthLabel(monthKey: string): string {
  const match = /^(\d{4})-(\d{2})$/.exec(monthKey);
  if (match === null) return monthKey;
  return `${match[1]} 年 ${Number(match[2])} 月`;
}

export type DayGroup = {
  /** `YYYY-MM-DD`, or the entry's raw value when it is not one. */
  date: string;
  label: string;
  weekday: string;
  today: boolean;
  entries: Entry[];
};

export type MonthGroup = { key: string; label: string; days: DayGroup[] };

/**
 * Groups the list into months and days, keeping the server's order (newest first) inside every group. The
 * groups themselves follow the order their first entry appears in, so no sorting rule is invented here.
 */
export function groupEntries(entries: Entry[], today: string = localToday()): MonthGroup[] {
  const months: MonthGroup[] = [];
  const days = new Map<string, DayGroup>();
  for (const entry of entries) {
    const date = entry.date;
    const key = monthKey(date);
    let month = months.find((candidate) => candidate.key === key);
    if (month === undefined) {
      month = { key, label: monthLabel(key), days: [] };
      months.push(month);
    }
    let day = days.get(date);
    if (day === undefined) {
      day = { date, label: dayLabel(date), weekday: weekdayLabel(date), today: date === today, entries: [] };
      days.set(date, day);
      month.days.push(day);
    }
    day.entries.push(entry);
  }
  return months;
}

function monthKey(date: string): string {
  const match = DATE_RE.exec(date);
  return match === null ? "未知月份" : `${match[1]}-${match[2]}`;
}

/** The digits a currency's minor unit has (JPY 0, CNY 2), read from the platform's own currency data. */
export function currencyDigits(currency: string): number {
  if (currency === "") return 2;
  try {
    const options = new Intl.NumberFormat("en", { style: "currency", currency }).resolvedOptions();
    return options.maximumFractionDigits ?? 2;
  } catch {
    return 2;
  }
}

/**
 * `¥86.00` in the currency's own format. A record without an amount and without a currency has nothing to show,
 * so it renders as the empty string and the card leaves the slot out.
 */
export function formatAmount(amountMinor: bigint, currency: string): string {
  if (amountMinor === 0n && currency === "") return "";
  const digits = currencyDigits(currency);
  const value = Number(amountMinor) / 10 ** digits;
  if (currency === "") return value.toFixed(digits);
  try {
    return new Intl.NumberFormat("zh-CN", { style: "currency", currency, currencyDisplay: "narrowSymbol" }).format(value);
  } catch {
    return `${currency} ${value.toFixed(digits)}`;
  }
}

/** The amount field's text as minor units, or null when it is not a number this currency can hold. */
export function parseAmountToMinor(input: string, currency: string): bigint | null {
  const value = input.trim().replaceAll(",", "");
  if (value === "") return 0n;
  if (!AMOUNT_RE.test(value)) return null;
  const digits = currencyDigits(currency);
  const [whole, fraction = ""] = value.split(".");
  if (fraction.length > digits) return null;
  const scale = 10n ** BigInt(digits);
  return BigInt(whole) * scale + BigInt(fraction.padEnd(digits, "0") || "0");
}

/** A short name for a time zone in the drawer: `上海 UTC+8`. */
const CITY_NAMES: Record<string, string> = {
  "Asia/Shanghai": "上海",
  "Asia/Hong_Kong": "香港",
  "Asia/Taipei": "台北",
  "Asia/Tokyo": "东京",
  "Asia/Seoul": "首尔",
  "Asia/Singapore": "新加坡",
  "Asia/Bangkok": "曼谷",
  "Asia/Kolkata": "加尔各答",
  "Europe/London": "伦敦",
  "Europe/Paris": "巴黎",
  "Europe/Berlin": "柏林",
  "America/New_York": "纽约",
  "America/Los_Angeles": "洛杉矶",
  "Australia/Sydney": "悉尼",
  UTC: "UTC",
};

export function timezoneLabel(timezone: string, at: Date = new Date()): string {
  const city = CITY_NAMES[timezone] ?? (timezone.split("/").pop() ?? timezone).replaceAll("_", " ");
  try {
    const offset = new Intl.DateTimeFormat("zh-CN", { timeZone: timezone, timeZoneName: "shortOffset" })
      .formatToParts(at)
      .find((part) => part.type === "timeZoneName")?.value;
    return offset === undefined ? city : `${city} ${offset.replace("GMT", "UTC")}`;
  } catch {
    return city;
  }
}

/** The drawer's own clock: the browser's time zone, offered as the record's default. */
export function browserTimezone(): string {
  const zone = new Intl.DateTimeFormat().resolvedOptions().timeZone;
  return zone === undefined || zone === "" ? "UTC" : zone;
}

/** The "记一笔" draft, as the form holds it (all strings, the amount in the field's text). */
export type DraftEntry = {
  title: string;
  date: string;
  time: string;
  timezone: string;
  category: string;
  location: string;
  amount: string;
  currency: string;
  note: string;
};

export function emptyDraft(clock: Date = new Date(), timezone: string = browserTimezone()): DraftEntry {
  return {
    title: "",
    date: localToday(clock),
    time: "",
    timezone,
    category: "",
    location: "",
    amount: "",
    currency: "CNY",
    note: "",
  };
}

export type DraftErrors = Partial<Record<keyof DraftEntry, string>>;

/**
 * The server's rules, checked before the round trip so the drawer can point at the field: a title (≤200 runes), a
 * real date, an optional `HH:MM`, a time zone name, and an amount this currency can hold (≤300 runes location,
 * ≤10000 runes note). Returns the minor-unit amount the request carries.
 */
export function validateDraft(draft: DraftEntry): { ok: true; amountMinor: bigint } | { ok: false; errors: DraftErrors } {
  const errors: DraftErrors = {};
  const title = draft.title.trim();
  if (title === "") errors.title = "请写下发生了什么";
  else if ([...title].length > 200) errors.title = "最多 200 个字";
  if (!isValidDate(draft.date)) errors.date = "选择有效日期";
  if (!isValidTime(draft.time)) errors.time = "时间格式为 HH:MM";
  if (draft.timezone.trim() === "") errors.timezone = "需要时区";
  if ([...draft.location].length > 300) errors.location = "地点最多 300 个字";
  if ([...draft.note].length > 10000) errors.note = "备注最多 10000 个字";
  if (draft.currency !== "" && !/^[A-Z]{3}$/.test(draft.currency)) errors.currency = "币种为三位大写字母";
  const amountMinor = parseAmountToMinor(draft.amount, draft.currency);
  if (amountMinor === null) errors.amount = `金额最多 ${currencyDigits(draft.currency)} 位小数`;
  if (Object.keys(errors).length > 0) return { ok: false, errors };
  return { ok: true, amountMinor: amountMinor ?? 0n };
}

/** The amount field's placeholder: the currency's own precision. */
export function amountPlaceholder(currency: string): string {
  const digits = currencyDigits(currency);
  return digits === 0 ? "0" : `0.${"0".repeat(digits)}`;
}
