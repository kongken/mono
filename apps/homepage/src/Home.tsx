import { useEffect } from "react";
import { ArrowDown, ArrowUpRight, Fingerprint, IdentificationBadge } from "@phosphor-icons/react";
import { SiteFooter, SiteHeader, focus } from "./site-chrome";

type Product = {
  title: string;
  category: string;
  description: string;
  meta: string;
  accent: "green" | "blue";
};

const products: Product[] = [
  {
    title: "山海足迹",
    category: "地理与旅行探索",
    description: "以省份探索和城市足迹，记录你的中国旅行地图。点亮去过的山川湖海，保留旅程的轻盈记忆。",
    meta: "14 省市 · 28 城市",
    accent: "green",
  },
  {
    title: "地铁足迹",
    category: "城市轨道记录",
    description: "记录乘坐过的线路与车站，收藏城市里的地铁足迹。把穿梭在地下与高架的日常通勤，变成专属的城市轨迹脉络。",
    meta: "6 条线路 · 82 座车站",
    accent: "blue",
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
  const href = product.accent === "blue" ? "https://metro.orz.ee" : "https://tripmap.orz.ee";
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
        <a className={`${focus} inline-flex items-center gap-2 rounded bg-primary px-4 py-2 text-sm text-primary-foreground transition-colors hover:bg-[var(--primary-container)]`} href={href} rel="noopener noreferrer">打开{product.title}<ArrowUpRight size={16} aria-hidden /></a>
        <span className="hidden font-mono text-[11px] tracking-[.04em] text-muted-foreground/70 sm:block">v{product.accent === "blue" ? "2.1 · metro.orz.ee" : "1.4 · tripmap.orz.ee"}</span>
      </div>
    </article>
  );
}

export default function HomePage() {
  useEffect(() => { document.title = "Oz · 独立数字产品工作室"; }, []);
  return (
    <div className="min-h-screen bg-[var(--surface)] text-foreground selection:bg-[var(--secondary-container)]">
      <SiteHeader current="home" />
      <main id="top" className="pt-16">
        <section className="mx-auto max-w-[1280px] px-5 pb-20 pt-12 md:px-12 md:pt-24">
          <div className="grid items-center gap-10 lg:grid-cols-12 lg:gap-12">
            <div className="flex flex-col items-start lg:col-span-7"><div className="mb-6 inline-flex items-center gap-2 rounded-full bg-[var(--surface-container-high)] px-3 py-1.5 font-mono text-[11px] tracking-[.04em] text-muted-foreground"><span className="relative h-2 w-2"><span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-[var(--secondary)] opacity-60" /><span className="relative inline-flex h-2 w-2 rounded-full bg-[var(--secondary)]" /></span>Oz · 小而用心的数字产品</div><h1 className="mb-6 max-w-[640px] font-serif text-[clamp(42px,6vw,72px)] leading-[1.08] tracking-[-.025em]">给日常，留一点探索的空间。</h1><p className="mb-7 max-w-[520px] text-lg leading-7 text-muted-foreground">记录走过的山海，也收藏城市里的每一段轨迹。无需打卡压力，只关乎纯粹的经历与抵达。</p><div className="flex items-center gap-5"><a href="#products" className={`${focus} inline-flex items-center gap-2 rounded bg-primary px-5 py-2.5 text-sm text-primary-foreground hover:bg-[var(--primary-container)]`}>探索产品<ArrowDown size={16} aria-hidden /></a></div></div>
            <div className="flex justify-center lg:col-span-5"><TopographicGraphic /></div>
          </div>
        </section>
        <section id="products" className="mx-auto max-w-[1280px] scroll-mt-20 px-5 pb-20 md:px-12"><div className="mb-7 flex flex-col justify-between gap-2 md:flex-row md:items-end"><div><div className="mb-1 font-mono text-[11px] uppercase tracking-[.12em] text-[var(--secondary)]">Products / 独立产品</div><h2 className="font-serif text-[28px] leading-9">两个围绕足迹与记录的 Web 探索工具</h2></div><p className="max-w-[360px] font-serif text-sm leading-5 text-muted-foreground">轻量设计，免去繁琐下载。随时开启浏览器，留存属于你自己的空间地图。</p></div><div className="grid gap-6 lg:grid-cols-2">{products.map((product) => <ProductCard key={product.title} product={product} />)}</div></section>
        <section id="ozid" className="mx-auto max-w-[1280px] scroll-mt-20 px-5 pb-20 md:px-12"><div className="flex flex-col items-start justify-between gap-4 rounded-xl bg-[var(--surface-low)] p-6 md:flex-row md:items-center md:p-8"><div className="flex items-start gap-4"><div className="grid h-10 w-10 shrink-0 place-items-center rounded-lg bg-[var(--surface-container-high)] text-primary"><IdentificationBadge size={20} aria-hidden /></div><div><div className="mb-1 flex flex-wrap items-center gap-2"><h2 className="font-serif text-xl">一个 OzID，连接不同的探索。</h2><span className="rounded bg-[var(--surface-container-high)] px-2 py-0.5 font-mono text-[11px] text-muted-foreground">统一通行证</span></div><p className="text-[13px] leading-5 text-muted-foreground">前往账号中心，管理你的个人足迹同步档案、安全密码与多端已登录设备。</p></div></div><a href="https://id.orz.ee" className={`${focus} inline-flex shrink-0 items-center gap-2 rounded bg-[var(--surface-container-highest)] px-4 py-2 font-mono text-[11px] text-foreground hover:bg-[var(--surface-container)]`}><Fingerprint size={16} aria-hidden />前往 OzID 账号中心</a></div></section>
      </main>
      <SiteFooter />
    </div>
  );
}
