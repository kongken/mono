import { Fingerprint } from "@phosphor-icons/react";

export const focus = "rounded-sm outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50";

export type SiteSection = "home" | "extensions" | "privacy";

type NavItem = {
  label: string;
  href: string;
  section?: SiteSection;
  hideOnMobile?: boolean;
};

const navItems: NavItem[] = [
  { label: "探索产品", href: "/#products", section: "home", hideOnMobile: true },
  { label: "Chrome 插件", href: "/extensions/", section: "extensions" },
  { label: "关于 Oz", href: "/#about", hideOnMobile: true },
  { label: "隐私政策", href: "/privacy/", section: "privacy", hideOnMobile: true },
];

export function SiteHeader({ current }: { current: SiteSection }) {
  const homeHref = current === "home" ? "#top" : "/";
  return (
    <header className="fixed left-0 top-0 z-50 w-full border-b border-border/40 bg-[color:var(--surface)/.9] backdrop-blur-md">
      <div className="mx-auto flex h-16 max-w-[1280px] items-center justify-between px-5 md:px-12">
        <a href={homeHref} className={`${focus} flex items-center gap-1.5`} aria-label="Oz Studio 首页">
          <span className="font-serif text-[28px] tracking-tight text-primary">Oz</span>
          <span className="h-1.5 w-1.5 rounded-full bg-[var(--secondary)]" />
          <span className="ml-1.5 hidden font-mono text-[11px] uppercase tracking-[.12em] text-muted-foreground sm:inline">Studio</span>
        </a>
        <nav aria-label="站点导航" className="flex items-center gap-3 md:gap-6">
          {navItems.map((item) => (
            <a
              key={item.label}
              href={item.href}
              aria-current={item.section === current ? "page" : undefined}
              className={`${focus} text-[13px] text-muted-foreground transition-colors hover:text-foreground aria-[current=page]:text-foreground ${item.hideOnMobile ? "hidden sm:inline" : ""}`}
            >
              {item.label}
            </a>
          ))}
          <a className={`${focus} rounded border border-border bg-[var(--surface-container)] px-2.5 py-1 font-mono text-[11px] text-muted-foreground hover:text-foreground`} href="https://id.orz.ee">OzID</a>
          <a className={`${focus} grid h-8 w-8 place-items-center rounded-full bg-primary text-primary-foreground`} href="https://id.orz.ee" aria-label="进入 OzID 登录"><Fingerprint size={18} aria-hidden /></a>
        </nav>
      </div>
    </header>
  );
}

export function SiteFooter() {
  return (
    <footer className="border-t border-border/40 bg-[var(--surface-low)] py-12">
      <div className="mx-auto flex max-w-[1280px] flex-col justify-between gap-6 px-5 md:flex-row md:items-baseline md:px-12">
        <div>
          <div className="flex items-center gap-1.5"><span className="font-serif text-xl text-primary">Oz</span><span className="text-[13px] text-muted-foreground">· 小而用心的数字产品</span></div>
          <p className="mt-2 font-serif text-sm italic text-muted-foreground">Crafted digital tools for journeys, cities, and calm reflection.</p>
        </div>
        <div className="flex flex-wrap items-center gap-5 text-[13px] text-muted-foreground">
          <a className={focus} href="/#products">山海足迹</a>
          <a className={focus} href="/#products">地铁足迹</a>
          <a className={focus} href="/extensions/">Chrome 插件</a>
          <a className={`${focus} font-mono text-[11px]`} href="https://id.orz.ee">OzID</a>
          <a className={focus} href="/privacy/">隐私政策</a>
          <span className="font-mono text-[11px]">© 2025 Oz Studio</span>
        </div>
      </div>
    </footer>
  );
}
