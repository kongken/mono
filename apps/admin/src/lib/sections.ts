// The sections of the console and the role that opens each. This only decides what is shown: a
// hidden menu entry is not an authorization, and the server checks the role again on every call.

export type Section = { to: string; label: string; role: string; hint: string };

/** metro catalogue sections join here when they move into the console; until then none is offered. */
export const SECTIONS: Section[] = [
  { to: "/users", label: "用户", role: "ozid-admin", hint: "按邮箱、用户 ID、昵称、状态和角色查询账号。" },
  { to: "/audit/users", label: "账号审计", role: "ozid-admin", hint: "查看账号安全与管理操作的记录：谁、对哪个账号、做了什么、结果如何。" },
];

export function sectionsFor(roles: readonly string[]): Section[] {
  return SECTIONS.filter((s) => roles.includes(s.role));
}

/** Roles the console knows how to describe; an account may hold others it has no section for. */
export const ADMIN_ROLES = ["ozid-admin", "metro-admin"];

export const isAdmin = (roles: readonly string[]) => roles.some((r) => ADMIN_ROLES.includes(r));
