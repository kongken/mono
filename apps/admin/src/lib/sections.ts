// The sections of the console and the role that opens each. This only decides what is shown: a
// hidden menu entry is not an authorization, and the server checks the role again on every call.

export type Section = { to: string; label: string; role: string; hint: string };

/** The metro catalogue administration; offered only while the metro upstream answers (see lib/metro.tsx). */
export const METRO_SECTION: Section = {
  to: "/metro",
  label: "地铁目录",
  role: "metro-admin",
  hint: "城市、线路、车型、专题的维护，目录导入导出、审计日志和管理令牌。",
};

export const SECTIONS: Section[] = [
  { to: "/users", label: "用户", role: "ozid-admin", hint: "按邮箱、用户 ID、昵称、状态和角色查询账号。" },
  { to: "/audit/users", label: "账号审计", role: "ozid-admin", hint: "查看账号安全与管理操作的记录：谁、对哪个账号、做了什么、结果如何。" },
  METRO_SECTION,
];

/**
 * What the menu and the home page offer: the sections of the roles the account holds, and the metro
 * administration only when metro is there to be administered (`metroAvailable`), so no section is a
 * pretence. This only decides what is shown; the server checks the role again on every call.
 */
export function sectionsFor(roles: readonly string[], metroAvailable = true): Section[] {
  return SECTIONS.filter((s) => roles.includes(s.role) && (s !== METRO_SECTION || metroAvailable));
}

/** Roles the console knows how to describe; an account may hold others it has no section for. */
export const ADMIN_ROLES = ["ozid-admin", "metro-admin"];

export const isAdmin = (roles: readonly string[]) => roles.some((r) => ADMIN_ROLES.includes(r));
