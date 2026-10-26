import { Notice } from "@kongken/account/ui";
import { Link } from "@/lib/router";
import { metroOffered, useMetro } from "@/lib/metro";
import { useRoles } from "@/lib/session";
import { ROLE_LABEL } from "@/lib/users";
import { sectionsFor } from "@/lib/sections";

export default function HomePage() {
  const roles = useRoles();
  const metro = useMetro();
  const sections = sectionsFor(roles, metroOffered(metro));
  return (
    <div className="grid gap-6">
      <h1 className="text-xl font-semibold">管理控制台</h1>
      <p className="text-sm text-muted-foreground">
        你的管理角色：
        <span data-testid="my-roles">{roles.filter((r) => r in ROLE_LABEL).map((r) => ROLE_LABEL[r]).join("、") || "无"}</span>
      </p>
      {sections.length > 0 ? (
        <ul className="grid gap-3 sm:grid-cols-2" data-testid="section-cards">
          {sections.map((s) => (
            <li key={s.to}>
              <Link to={s.to} className="block rounded-lg border bg-card p-4 outline-none hover:bg-muted focus-visible:ring-[3px] focus-visible:ring-ring/50">
                <span className="font-medium">{s.label}</span>
                <span className="mt-1 block text-sm text-muted-foreground">{s.hint}</span>
              </Link>
            </li>
          ))}
        </ul>
      ) : (
        <Notice kind="info">
          {roles.includes("metro-admin") && metro === "absent"
            ? "这个环境没有部署地铁目录服务，所以没有可管理的分区。"
            : roles.includes("metro-admin") && metro === "checking"
              ? "正在确认可用的分区…"
              : "你的角色在控制台里暂时没有可用的分区。"}
        </Notice>
      )}
    </div>
  );
}
