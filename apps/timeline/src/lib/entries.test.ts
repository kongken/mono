import assert from "node:assert/strict";
import test from "node:test";
import type { Entry } from "@kongken/api-client/bff/timeline/v1/timeline_pb";
import {
  EMPTY_FILTERS,
  activeFilters,
  amountPlaceholder,
  currencyDigits,
  dayLabel,
  emptyDraft,
  formatAmount,
  groupEntries,
  isValidDate,
  isValidTime,
  listRequest,
  localToday,
  monthLabel,
  parseAmountToMinor,
  timezoneLabel,
  validateDraft,
  weekdayLabel,
} from "./entries.ts";

const entry = (date: string, title: string): Entry => ({ date, title }) as Entry;
const clock = new Date(2026, 10, 7, 12, 30); // 2026-11-07 12:30 local

test("localToday writes the local calendar day", () => {
  assert.equal(localToday(clock), "2026-11-07");
  assert.equal(localToday(new Date(2026, 0, 9)), "2026-01-09");
});

test("a date is valid only when it is a real YYYY-MM-DD day", () => {
  assert.equal(isValidDate("2026-11-07"), true);
  assert.equal(isValidDate("2026-02-30"), false);
  assert.equal(isValidDate("2026-2-3"), false);
  assert.equal(isValidDate(""), false);
});

test("a time is HH:MM or HH:MM:SS, and empty means no time", () => {
  assert.equal(isValidTime(""), true);
  assert.equal(isValidTime("09:05"), true);
  assert.equal(isValidTime("23:59:59"), true);
  assert.equal(isValidTime("24:00"), false);
  assert.equal(isValidTime("9:05"), false);
});

test("day, weekday and month labels read the calendar day itself", () => {
  assert.equal(dayLabel("2026-11-07"), "11 月 7 日");
  assert.equal(dayLabel("2026-01-01"), "1 月 1 日");
  assert.equal(weekdayLabel("2026-11-07"), "星期六");
  assert.equal(weekdayLabel("2026-11-08"), "星期日");
  assert.equal(monthLabel("2026-11"), "2026 年 11 月");
});

test("entries keep the server's order and group by month, then by day", () => {
  const months = groupEntries(
    [entry("2026-11-07", "和朋友吃午饭"), entry("2026-11-07", "周末理发"), entry("2026-11-01", "江边慢跑"), entry("2026-10-31", "清洗空调滤网")],
    "2026-11-07",
  );
  assert.deepEqual(
    months.map((month) => month.key),
    ["2026-11", "2026-10"],
  );
  assert.deepEqual(
    months[0]?.days.map((day) => [day.date, day.entries.length, day.today]),
    [
      ["2026-11-07", 2, true],
      ["2026-11-01", 1, false],
    ],
  );
  assert.equal(months[1]?.label, "2026 年 10 月");
  assert.equal(months[0]?.days[0]?.weekday, "星期六");
  assert.equal(months[0]?.days[1]?.label, "11 月 1 日");
});

test("a month that comes back later joins the group it already has", () => {
  const months = groupEntries([entry("2026-11-07", "a"), entry("2026-10-31", "b"), entry("2026-11-06", "c")]);
  assert.deepEqual(
    months.map((month) => month.key),
    ["2026-11", "2026-10"],
  );
  assert.deepEqual(
    months[0]?.days.map((day) => day.date),
    ["2026-11-07", "2026-11-06"],
  );
});

test("the empty list has no groups", () => {
  assert.deepEqual(groupEntries([]), []);
});

test("an amount is written in its currency, digits and all", () => {
  assert.equal(formatAmount(8600n, "CNY").includes("86.00"), true);
  assert.equal(formatAmount(23900n, "CNY").includes("239.00"), true);
  assert.equal(formatAmount(342n, "JPY").includes("342"), true);
  assert.equal(formatAmount(0n, ""), "");
  assert.equal(formatAmount(1200n, ""), "12.00");
});

test("currency precision comes from the currency, not from a fixed two digits", () => {
  assert.equal(currencyDigits("CNY"), 2);
  assert.equal(currencyDigits("JPY"), 0);
  assert.equal(currencyDigits("BHD"), 3);
  assert.equal(currencyDigits("XX"), 2);
  assert.equal(amountPlaceholder("CNY"), "0.00");
  assert.equal(amountPlaceholder("JPY"), "0");
});

test("the amount field becomes minor units, or null when the currency cannot hold it", () => {
  assert.equal(parseAmountToMinor("86.5", "CNY"), 8650n);
  assert.equal(parseAmountToMinor("86", "CNY"), 8600n);
  assert.equal(parseAmountToMinor("1,234.56", "CNY"), 123456n);
  assert.equal(parseAmountToMinor("86.555", "CNY"), null);
  assert.equal(parseAmountToMinor("abc", "CNY"), null);
  assert.equal(parseAmountToMinor("", "CNY"), 0n);
  assert.equal(parseAmountToMinor("12.5", "JPY"), null);
  assert.equal(parseAmountToMinor("12", "JPY"), 12n);
});

test("the time zone reads as a city and its offset", () => {
  const at = new Date("2026-11-07T04:00:00Z");
  assert.equal(timezoneLabel("Asia/Shanghai", at), "上海 UTC+8");
  assert.equal(timezoneLabel("UTC", at).startsWith("UTC UTC"), true);
  assert.equal(timezoneLabel("America/New_York", at), "纽约 UTC-5");
  assert.equal(timezoneLabel("Asia/Kathmandu", at), "Kathmandu UTC+5:45");
});

test("the filter bar lists only the filters in effect", () => {
  assert.deepEqual(activeFilters(EMPTY_FILTERS), []);
  assert.deepEqual(activeFilters({ ...EMPTY_FILTERS, category: " 外出就餐 " }), [{ key: "category", label: "分类：外出就餐" }]);
  assert.deepEqual(activeFilters({ ...EMPTY_FILTERS, fromDate: "2026-11-01" }), [{ key: "fromDate", label: "日期：2026-11-01 起" }]);
  assert.deepEqual(activeFilters({ ...EMPTY_FILTERS, fromDate: "2026-11-01", toDate: "2026-11-07" }), [
    { key: "fromDate", label: "日期：2026-11-01 ~ 2026-11-07" },
  ]);
  assert.deepEqual(activeFilters({ ...EMPTY_FILTERS, query: " 午饭 ", toDate: "2026-11-07" }), [
    { key: "query", label: "搜索：午饭" },
    { key: "fromDate", label: "日期：截至 2026-11-07" },
  ]);
});

test("a list request carries trimmed filters and the view's delete flag", () => {
  assert.deepEqual(listRequest({ query: "  午饭 ", category: "", fromDate: "", toDate: "" }, false), {
    query: "午饭",
    category: "",
    fromDate: "",
    toDate: "",
    includeDeleted: false,
  });
  assert.equal(listRequest(EMPTY_FILTERS, true).includeDeleted, true);
});

test("the draft defaults to today, no time and the browser's zone", () => {
  const draft = emptyDraft(clock, "Asia/Shanghai");
  assert.equal(draft.date, "2026-11-07");
  assert.equal(draft.time, "");
  assert.equal(draft.timezone, "Asia/Shanghai");
  assert.equal(draft.currency, "CNY");
});

test("a draft the server would refuse is refused here, on the field", () => {
  const good = { ...emptyDraft(clock, "Asia/Shanghai"), title: "和朋友吃午饭", amount: "86.00", currency: "CNY" };
  assert.deepEqual(validateDraft(good), { ok: true, amountMinor: 8600n });

  const noTitle = validateDraft({ ...good, title: "   " });
  assert.equal(noTitle.ok, false);
  assert.equal(noTitle.ok === false && noTitle.errors.title, "请写下发生了什么");

  const badDate = validateDraft({ ...good, date: "2026-02-30" });
  assert.equal(badDate.ok === false && badDate.errors.date, "选择有效日期");

  const badTime = validateDraft({ ...good, time: "9:5" });
  assert.equal(badTime.ok === false && badTime.errors.time, "时间格式为 HH:MM");

  const badAmount = validateDraft({ ...good, amount: "8.555" });
  assert.equal(badAmount.ok === false && badAmount.errors.amount, "金额最多 2 位小数");

  const badCurrency = validateDraft({ ...good, currency: "cny" });
  assert.equal(badCurrency.ok === false && badCurrency.errors.currency, "币种为三位大写字母");

  const noZone = validateDraft({ ...good, timezone: "" });
  assert.equal(noZone.ok === false && noZone.errors.timezone, "需要时区");
});
