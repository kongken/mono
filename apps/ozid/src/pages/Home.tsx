import { useEffect, type ComponentType } from "react";
import { ArrowDown, ArrowUpRight, Fingerprint, IdentificationBadge, Mountains, Train, type IconProps } from "@phosphor-icons/react";
import { navQuery } from "@kongken/account/navigation";
import { useTarget } from "@/lib/target";

const focus = "rounded-sm outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50";

type Product = {
  appId: string;
  title: string;
  category: string;
  description: string;
  meta: string;
  accent: "green" | "blue";
  icon: ComponentType<IconProps>;
};

const products: Product[] = [
  {
    appId: "trip-map",
    title: "山海足迹",
    category: "地理与旅行探索",
    description: "以省份探索和城市足迹，记录你的中国旅行地图。点亮去过的山川湖海，保留旅程的轻盈记忆。",
    meta: "14 省市 · 28 城市",
    accent: "green",
    icon: Mountains,
  },
  {
    appId: "metro",
    title: "地铁足迹",
    category: "城市轨道记录",
    description: "记录乘坐过的线路与车站，收藏城市里的地铁足迹。把穿梭在地下与高架的日常通勤，变成专属的城市轨迹脉络。",
    meta: "6 条线路 · 82 座车站",
    accent: "blue",
    icon: Train,
  },
];

function TopographicGraphic() {
  return (
    <div className="relative aspect-square w-full max-w-[440px] overflow-hidden rounded-xl bg-[var(--surface-low)] p-4">
      <svg className="h-full w-full text-[var(--secondary)] opacity-70" fill="none" viewBox="0 0 400 400" aria-label="地形与城市线路抽象图" role="img">
        <path d="M60 200C60 110 120 60 210 60s140 60 130 150-70 130-160 130c-80 0-120-60-120-140Z" stroke="currentColor" strokeDasharray="3 3" />
        <path d="M90 200c0-70 50-110 120-110s110 50 100 120-60 100-130 100c-60 0-90-50-90-110Z" stroke="currentColor" />
        <path d="M120 200c0-50 40-80 90-80s70 40 70 90-40 70-95 70c-45 0-65-40-65-80Z" stroke="currentColor" strokeDasharray="4 4" />
        <path d="M150 200c0-30 20-50 55-50s45 25 45 55-25 45-60 45-40-25-40-50Z" stroke="currentColor" strokeWidth="1.2" />
        <path d="M40 280l100-80h120l70-70" stroke="var(--tertiary)" strokeLinecap="round" strokeLinejoin="round" strokeWidth="2.5" />
        <path d="M180 80v120l100 100" stroke="var(--outline)" strokeDasharray="6 4" strokeLinecap="round" strokeWidth="1.5" />
        <circle cx="140" cy="200" r="5" fill="var(--surface)" stroke="var(--tertiary)" strokeWidth="2" />
        <circle cx="180" cy="200" r="7" fill="var(--surface)" stroke="var(--primary)" strokeWidth="2.5" />
        <circle cx="180" cy="200" r="3" fill="var(--primary)" />
        <circle cx="260" cy="200" r="5" fill="var(--surface)" stroke="var(--tertiary)" strokeWidth="2" />
        <circle cx="330" cy="130" r="4" fill="var(--secondary)" />
        <text fill="var(--muted-foreground)" fontFamily="'JetBrains Mono', monospace" fontSize="10" letterSpacing=".1em" x="30" y="50">GRID: 31°14'N 121°28'E</text>
        <text fill="var(--muted-foreground)" fontFamily="'JetBrains Mono', monospace" fontSize="10" letterSpacing=".1em" x="300" y="365">ELEV. 142M</text>
      </svg>
      <span className="absolute bottom-4 left-4 font-mono text-[11px] tracking-[.04em] text-muted-foreground/70">FIG. 01 — DUAL TRACKS</span>
    </div>
  );
}

function ProductVisual({ product }: { product: Product }) {
  const metro = product.accent === "blue";
  return (
    <div className="relative mb-6 h-56 overflow-hidden rounded-lg bg-[var(--surface-lowest)] p-4">
      <svg className="absolute inset-0 h-full w-full" fill="none" viewBox="0 0 500 240" aria-hidden="true">
        {metro ? (
          <>
            <path d="M40 160h110l70-70h240" stroke="var(--tertiary)" strokeLinecap="round" strokeLinejoin="round" strokeWidth="3" />
            <path d="M80 40l140 140h140l80-80" stroke="var(--outline)" strokeLinecap="round" strokeLinejoin="round" strokeWidth="3" />
            {["150,160", "340,90", "410,90", "220,180", "310,180", "150,110"].map((point) => { const [cx, cy] = point.split(","); return <circle key={point} cx={cx} cy={cy} r="4" fill="var(--surface)" stroke="var(--tertiary)" strokeWidth="2" />; })}
            <text fill="var(--muted-foreground)" fontFamily="DM Sans, sans-serif" fontSize="9" x="135" y="180">人民广场</text>
            <text fill="var(--muted-foreground)" fontFamily="DM Sans, sans-serif" fontSize="9" x="330" y="78">世纪大道</text>
            <text fill="var(--muted-foreground)" fontFamily="DM Sans, sans-serif" fontSize="9" x="235" y="195">静安寺</text>
          </>
        ) : (
          <>
            <path d="M50 180Q90 130 160 140t100-30t100-40t90 60v100H30V230Z" fill="var(--secondary)" opacity=".35" />
            <path d="M110 160q60-40 120-30t90-40t100 30" stroke="var(--tertiary)" strokeDasharray="3 3" strokeWidth="1.5" />
            <path d="M80 195q80-35 170-30t130-30t60 15" stroke="var(--tertiary)" />
            <path d="M160 140l100-30 60-20 40 45" stroke="var(--primary)" strokeDasharray="4 2" strokeWidth="1.5" />
            {["160,140,4", "260,110,5", "320,90,4", "360,70,6", "410,115,4"].map((point) => { const [cx, cy, r] = point.split(","); return <circle key={point} cx={cx} cy={cy} r={r} fill="var(--primary)" />; })}
          </>
        )}
      </svg>
      <div className="relative z-10 flex items-center justify-between font-mono text-[11px] tracking-[.04em] text-muted-foreground">
        <span>{metro ? "METRO TRANSIT 02" : "EXP. ATLAS 01"}</span>
        <span className="rounded bg-[var(--surface-container)] px-2 py-0.5">{metro ? "城市路网" : "全国概览"}</span>
      </div>
      <div className="relative z-10 flex items-end justify-between pt-32 font-mono text-[13px]">
        <div><div className="font-serif text-sm text-muted-foreground">{metro ? "打卡站点" : "点亮进度"}</div><strong>{product.meta}</strong></div>
        <div className="text-right"><div className="font-serif text-sm text-muted-foreground">{metro ? "城市索引" : "覆盖率"}</div><strong className={metro ? "text-[var(--tertiary)]" : "text-[var(--secondary)]"}>{metro ? "SH / BJ / GZ" : "41.2%"}</strong></div>
      </div>
    </div>
  );
}

function ProductCard({ product }: { product: Product }) {
  const target = useTarget(navQuery({ appId: product.appId }));
  const href = target.status === "ok" ? target.target.returnUrl : undefined;
  return (
    <article className="group flex flex-col justify-between rounded-xl bg-[var(--surface-low)] p-6 transition-colors hover:bg-[var(--surface-container)] md:p-8">
      <div>
        <div className="mb-4 flex items-center justify-between gap-3 font-mono text-[11px] tracking-[.04em] text-muted-foreground">
          <span className={`rounded px-2 py-0.5 ${product.accent === "blue" ? "bg-[var(--tertiary-fixed)] text-[var(--tertiary)]" : "bg-[var(--secondary-container)] text-[var(--secondary-foreground)]"}`}>{product.category}</span>
          <span className="flex items-center gap-1.5 whitespace-nowrap"><span className={`h-1.5 w-1.5 rounded-full ${product.accent === "blue" ? "bg-[var(--tertiary)]" : "bg-[var(--secondary)]"}`} />已就绪 · Web 端</span>
        </div>
        <h3 className="mb-2 font-serif text-[28px] leading-9 tracking-tight">{product.title}</h3>
        <p className="mb-6 text-[15px] leading-6 text-muted-foreground">{product.description}</p>
        <ProductVisual product={product} />
      </div>
      <div className="flex items-center justify-between gap-3 pt-2">
        {href ? <a className={`${focus} inline-flex items-center gap-2 rounded bg-primary px-4 py-2 text-sm text-primary-foreground transition-colors hover:bg-[var(--primary-container)]`} href={href} rel="noopener noreferrer">打开{product.title}<ArrowUpRight size={16} aria-hidden /></a> : <span className="text-sm text-muted-foreground">{target.status === "resolving" ? "正在读取入口…" : "当前未提供此产品入口"}</span>}
        <span className="hidden font-mono text-[11px] tracking-[.04em] text-muted-foreground/70 sm:block">v{product.accent === "blue" ? "2.1 · metro.oz.studio" : "1.4 · shanhai.oz.studio"}</span>
      </div>
    </article>
  );
}

export default function HomePage() {
  useEffect(() => { document.title = "Oz · 独立数字产品工作室"; }, []);
  return (
    <div className="min-h-screen bg-[var(--surface)] text-foreground selection:bg-[var(--secondary-container)]">
      <header className="fixed left-0 top-0 z-50 w-full border-b border-border/40 bg-[color:var(--surface)/.9] backdrop-blur-md">
        <div className="mx-auto flex h-16 max-w-[1280px] items-center justify-between px-5 md:px-12">
          <a href="#top" className={`${focus} flex items-center gap-1.5`}><span className="font-serif text-[28px] tracking-tight text-primary">Oz</span><span className="h-1.5 w-1.5 rounded-full bg-[var(--secondary)]" /><span className="ml-1.5 hidden font-mono text-[11px] uppercase tracking-[.12em] text-muted-foreground sm:inline">Studio</span></a>
          <nav aria-label="主页" className="flex items-center gap-4 md:gap-7"><a className={`${focus} text-[13px] text-muted-foreground hover:text-foreground`} href="#products">探索产品</a><a className={`${focus} text-[13px] text-muted-foreground hover:text-foreground`} href="#about">关于 Oz</a><a className={`${focus} rounded border border-border bg-[var(--surface-container)] px-2.5 py-1 font-mono text-[11px] text-muted-foreground hover:text-foreground`} href="/login">OzID</a><a className={`${focus} grid h-8 w-8 place-items-center rounded-full bg-primary text-primary-foreground`} href="/login" aria-label="进入 OzID 登录"><Fingerprint size={18} aria-hidden /></a></nav>
        </div>
      </header>
      <main id="top" className="pt-16">
        <section className="mx-auto max-w-[1280px] px-5 pb-20 pt-12 md:px-12 md:pt-24">
          <div className="grid items-center gap-10 lg:grid-cols-12 lg:gap-12">
            <div className="flex flex-col items-start lg:col-span-7"><div className="mb-6 inline-flex items-center gap-2 rounded-full bg-[var(--surface-container-high)] px-3 py-1.5 font-mono text-[11px] tracking-[.04em] text-muted-foreground"><span className="relative h-2 w-2"><span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-[var(--secondary)] opacity-60" /><span className="relative inline-flex h-2 w-2 rounded-full bg-[var(--secondary)]" /></span>Oz · 小而用心的数字产品</div><h1 className="mb-6 max-w-[640px] font-serif text-[clamp(42px,6vw,72px)] leading-[1.08] tracking-[-.025em]">给日常，留一点探索的空间。</h1><p className="mb-7 max-w-[520px] text-lg leading-7 text-muted-foreground">记录走过的山海，也收藏城市里的每一段轨迹。无需打卡压力，只关乎纯粹的经历与抵达。</p><div className="flex items-center gap-5"><a href="#products" className={`${focus} inline-flex items-center gap-2 rounded bg-primary px-5 py-2.5 text-sm text-primary-foreground hover:bg-[var(--primary-container)]`}>探索产品<ArrowDown size={16} aria-hidden /></a><a href="#about" className={`${focus} text-sm text-muted-foreground underline decoration-border underline-offset-4 hover:text-foreground`}>认识 Oz</a></div></div>
            <div className="flex justify-center lg:col-span-5"><TopographicGraphic /></div>
          </div>
        </section>
        <section id="products" className="mx-auto max-w-[1280px] scroll-mt-20 px-5 pb-20 md:px-12"><div className="mb-7 flex flex-col justify-between gap-2 md:flex-row md:items-end"><div><div className="mb-1 font-mono text-[11px] uppercase tracking-[.12em] text-[var(--secondary)]">Products / 独立产品</div><h2 className="font-serif text-[28px] leading-9">两个围绕足迹与记录的 Web 探索工具</h2></div><p className="max-w-[360px] font-serif text-sm leading-5 text-muted-foreground">轻量设计，免去繁琐下载。随时开启浏览器，留存属于你自己的空间地图。</p></div><div className="grid gap-6 lg:grid-cols-2">{products.map((product) => <ProductCard key={product.appId} product={product} />)}</div></section>
        <section id="ozid" className="mx-auto max-w-[1280px] scroll-mt-20 px-5 pb-20 md:px-12"><div className="flex flex-col items-start justify-between gap-4 rounded-xl bg-[var(--surface-low)] p-6 md:flex-row md:items-center md:p-8"><div className="flex items-start gap-4"><div className="grid h-10 w-10 shrink-0 place-items-center rounded-lg bg-[var(--surface-container-high)] text-primary"><IdentificationBadge size={20} aria-hidden /></div><div><div className="mb-1 flex flex-wrap items-center gap-2"><h2 className="font-serif text-xl">一个 OzID，连接不同的探索。</h2><span className="rounded bg-[var(--surface-container-high)] px-2 py-0.5 font-mono text-[11px] text-muted-foreground">统一通行证</span></div><p className="text-[13px] leading-5 text-muted-foreground">前往账号中心，管理你的个人足迹同步档案、安全密码与多端已登录设备。</p></div></div><a href="/login" className={`${focus} inline-flex shrink-0 items-center gap-2 rounded bg-[var(--surface-container-highest)] px-4 py-2 font-mono text-[11px] text-foreground hover:bg-[var(--surface-container)]`}><Fingerprint size={16} aria-hidden />前往 OzID 账号中心</a></div></section>
        <section id="about" className="mx-auto max-w-[1280px] scroll-mt-20 px-5 pb-20 md:px-12"><div className="border-t border-border/50 pt-8"><div className="mb-2 font-mono text-[11px] uppercase tracking-[.12em] text-[var(--secondary)]">Philosophy / 设计原点</div><div className="grid items-start gap-8 lg:grid-cols-12"><div className="lg:col-span-8"><p className="mb-5 font-serif text-[28px] leading-relaxed">Oz 始于一个单纯的念头：为自己的日常做一些真正趁手、安静、没有压迫感的小工具。不追求无谓的停留时长与繁复功能，只愿在喧嚣的互联网角落，为你留下几分记录与探索的趣味。</p><p className="text-[15px] leading-6 text-muted-foreground">我们相信优秀的数字工具应当像一本做工考究的布面旅行日记——它不必时刻打扰你，只在翻开时给予准确、愉悦的反馈。所有的足迹数据均支持清晰的自主导出，陪伴你的每一步路。</p></div><div className="grid gap-2 lg:col-span-4">{[["克制设计","没有开屏广告，没有冗余弹窗。去除一切干扰，只保留清晰直观的地图与线路视图。"],["专注记录","不追求社交攀比与排名机制。只关乎你在某一年走过的省份、坐过的晚班地铁。"],["独立长存","小而轻便的微型架构。承诺数据开放导出，保持自给自足与长期稳定的服务维护。"]].map(([title, text], index) => <div key={title} className="rounded-lg bg-[var(--surface-low)] p-4"><div className="mb-1 flex items-center gap-2 text-[15px] font-medium"><span className={`h-1.5 w-1.5 rounded-full ${index === 0 ? "bg-primary" : index === 1 ? "bg-[var(--secondary)]" : "bg-[var(--tertiary)]"}`} />{title}</div><p className="font-serif text-sm leading-5 text-muted-foreground">{text}</p></div>)}</div></div></div></section>
      </main>
      <footer className="border-t border-border/40 bg-[var(--surface-low)] py-12"><div className="mx-auto flex max-w-[1280px] flex-col justify-between gap-6 px-5 md:flex-row md:items-baseline md:px-12"><div><div className="flex items-center gap-1.5"><span className="font-serif text-xl text-primary">Oz</span><span className="text-[13px] text-muted-foreground">· 小而用心的数字产品</span></div><p className="mt-2 font-serif text-sm italic text-muted-foreground">Crafted digital tools for journeys, cities, and calm reflection.</p></div><div className="flex flex-wrap items-center gap-5 text-[13px] text-muted-foreground"><a className={focus} href="#products">山海足迹</a><a className={focus} href="#products">地铁足迹</a><a className={`${focus} font-mono text-[11px]`} href="/login">OzID</a><span className="font-mono text-[11px]">© 2025 Oz Studio</span></div></div></footer>
    </div>
  );
}
