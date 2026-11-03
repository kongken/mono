import { useEffect } from "react";
import { ArrowLeft, ArrowUpRight, CheckCircle, PuzzlePiece } from "@phosphor-icons/react";
import { extensions, type ChromeExtension } from "./extensions";
import { SiteFooter, SiteHeader, focus } from "./site-chrome";

function ExtensionCard({ extension }: { extension: ChromeExtension }) {
  return (
    <article className="rounded-xl bg-[var(--surface-low)] p-6 md:p-8">
      <div className="flex flex-col gap-5 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex items-start gap-4">
          <img
            src={extension.icon}
            alt={`${extension.name} 图标`}
            width={64}
            height={64}
            className="h-16 w-16 shrink-0 rounded-xl border border-border/50 bg-[var(--surface-lowest)]"
          />
          <div>
            <div className="mb-1.5 flex flex-wrap items-center gap-2 font-mono text-[11px] tracking-[.04em] text-muted-foreground">
              <span className="rounded bg-[var(--secondary-container)] px-2 py-0.5 text-[var(--secondary-foreground)]">{extension.category}</span>
              <span>v{extension.version}</span>
            </div>
            <h2 className="font-serif text-[26px] leading-8 tracking-tight md:text-[28px]">{extension.name}</h2>
            <p className="mt-1 max-w-[520px] text-[15px] leading-6 text-muted-foreground">{extension.tagline}</p>
          </div>
        </div>
        <a
          href={extension.storeUrl}
          target="_blank"
          rel="noopener noreferrer"
          className={`${focus} inline-flex shrink-0 items-center justify-center gap-2 rounded bg-primary px-4 py-2.5 text-sm text-primary-foreground transition-colors hover:bg-[var(--primary-container)]`}
        >
          在 Chrome 网上应用店查看<ArrowUpRight size={16} aria-hidden />
        </a>
      </div>

      <div className="mt-7 grid gap-6 lg:grid-cols-[1.05fr_.95fr] lg:gap-8">
        <div>
          <h3 className="mb-3 font-mono text-[11px] uppercase tracking-[.12em] text-[var(--secondary)]">Features / 主要功能</h3>
          <ul className="grid gap-2.5 sm:grid-cols-2 lg:grid-cols-1 xl:grid-cols-2">
            {extension.features.map((feature) => (
              <li key={feature} className="flex gap-2 text-[14px] leading-5 text-muted-foreground">
                <CheckCircle size={16} weight="duotone" className="mt-0.5 shrink-0 text-[var(--secondary)]" aria-hidden />
                <span>{feature}</span>
              </li>
            ))}
          </ul>
        </div>
        <figure className="m-0 overflow-hidden rounded-lg border border-border/60 bg-[var(--surface-lowest)]">
          <img src={extension.screenshot} alt={`${extension.name} 商店截图`} width={1280} height={800} loading="lazy" className="block w-full" />
          <figcaption className="border-t border-border/50 px-3 py-2 font-mono text-[11px] tracking-[.04em] text-muted-foreground">商店截图 · {extension.name}</figcaption>
        </figure>
      </div>

      <div className="mt-7 grid gap-4 border-t border-border/50 pt-6 md:grid-cols-2">
        <div className="rounded-lg bg-[var(--surface-lowest)] p-4">
          <h3 className="mb-2 font-mono text-[11px] uppercase tracking-[.12em] text-[var(--secondary)]">Permissions / 权限说明</h3>
          <ul className="grid gap-1.5 text-[13px] leading-5 text-muted-foreground">
            {extension.permissions.map((permission) => <li key={permission}>{permission}</li>)}
          </ul>
        </div>
        <div className="rounded-lg bg-[var(--surface-lowest)] p-4">
          <h3 className="mb-2 font-mono text-[11px] uppercase tracking-[.12em] text-[var(--secondary)]">Privacy / 数据与隐私</h3>
          <ul className="grid gap-1.5 text-[13px] leading-5 text-muted-foreground">
            {extension.privacy.map((item) => <li key={item}>{item}</li>)}
          </ul>
        </div>
      </div>

      <dl className="mt-5 flex flex-wrap items-center gap-x-5 gap-y-2 font-mono text-[11px] tracking-[.04em] text-muted-foreground/80">
        <div className="flex gap-1.5"><dt>更新</dt><dd className="m-0">{extension.updated}</dd></div>
        <div className="flex gap-1.5"><dt>大小</dt><dd className="m-0">{extension.size}</dd></div>
        <div className="flex gap-1.5"><dt>语言</dt><dd className="m-0">{extension.languages}</dd></div>
        <div className="flex gap-1.5"><dt>开发者</dt><dd className="m-0">{extension.developer}</dd></div>
        <div className="flex gap-1.5"><dt>扩展 ID</dt><dd className="m-0 break-all">{extension.id}</dd></div>
      </dl>
    </article>
  );
}

const installSteps = [
  ["打开商店页面", "点击插件卡片上的按钮，在 Chrome 网上应用店确认插件名称与开发者 OzLabs。"],
  ["添加到 Chrome", "点「添加至 Chrome」，在浏览器的确认弹窗中核对权限后安装。"],
  ["固定到工具栏", "点击工具栏的拼图图标，把插件固定出来，随时开关配置档案或一键暂停规则。"],
];

export default function ExtensionsPage() {
  useEffect(() => { document.title = "Chrome 插件 · Oz Studio"; }, []);
  return (
    <div className="min-h-screen bg-[var(--surface)] text-foreground selection:bg-[var(--secondary-container)]">
      <SiteHeader current="extensions" />
      <main id="top" className="pt-16">
        <section className="mx-auto max-w-[1280px] px-5 pb-14 pt-12 md:px-12 md:pt-20">
          <a href="/" className={`${focus} inline-flex items-center gap-1.5 font-mono text-[11px] tracking-[.04em] text-muted-foreground hover:text-foreground`}>
            <ArrowLeft size={14} aria-hidden />返回首页
          </a>
          <div className="mt-7 mb-3 font-mono text-[11px] uppercase tracking-[.12em] text-[var(--secondary)]">Extensions / Chrome 插件</div>
          <div className="grid items-end gap-8 lg:grid-cols-12">
            <div className="lg:col-span-7">
              <h1 className="mb-5 max-w-[620px] font-serif text-[clamp(38px,5vw,60px)] leading-[1.1] tracking-[-.02em]">给浏览器，加一点趁手的工具。</h1>
              <p className="max-w-[540px] text-lg leading-7 text-muted-foreground">Oz Studio 在 Chrome 网上应用店发布的插件都收在这里。它们都只解决一件小事：把每天要重复的浏览器操作，变得安静、准确、一步到位。</p>
            </div>
            <div className="flex flex-wrap items-center gap-2 lg:col-span-5 lg:justify-end">
              {[`${extensions.length} 个已上架`, "Chrome 桌面版", "无需额外账号"].map((item) => (
                <span key={item} className="rounded-full bg-[var(--surface-container-high)] px-3 py-1.5 font-mono text-[11px] tracking-[.04em] text-muted-foreground">{item}</span>
              ))}
            </div>
          </div>
        </section>

        <section id="list" className="mx-auto max-w-[1280px] scroll-mt-20 px-5 pb-20 md:px-12">
          <div className="mb-7 flex flex-col justify-between gap-2 md:flex-row md:items-end">
            <div>
              <div className="mb-1 font-mono text-[11px] uppercase tracking-[.12em] text-[var(--secondary)]">Listings / 已上架</div>
              <h2 className="font-serif text-[28px] leading-9">在 Chrome 网上应用店可以直接安装</h2>
            </div>
            <p className="max-w-[360px] font-serif text-sm leading-5 text-muted-foreground">新插件上架后会加进这份列表，版本、更新时间与权限说明都以商店页面为准。</p>
          </div>
          <div className="grid gap-6">
            {extensions.map((extension) => <ExtensionCard key={extension.id} extension={extension} />)}
          </div>
        </section>

        <section id="install" className="mx-auto max-w-[1280px] scroll-mt-20 px-5 pb-20 md:px-12">
          <div className="rounded-xl bg-[var(--surface-low)] p-6 md:p-8">
            <div className="mb-6 flex items-center gap-3">
              <div className="grid h-10 w-10 shrink-0 place-items-center rounded-lg bg-[var(--surface-container-high)] text-primary"><PuzzlePiece size={20} aria-hidden /></div>
              <div>
                <div className="mb-1 font-mono text-[11px] uppercase tracking-[.12em] text-[var(--secondary)]">Install / 安装</div>
                <h2 className="font-serif text-xl">三步装好一个插件</h2>
              </div>
            </div>
            <ol className="grid gap-4 md:grid-cols-3">
              {installSteps.map(([title, text], index) => (
                <li key={title} className="rounded-lg bg-[var(--surface-lowest)] p-4">
                  <div className="mb-1.5 flex items-center gap-2 text-[15px] font-medium">
                    <span className="font-mono text-[11px] text-[var(--secondary)]">0{index + 1}</span>
                    {title}
                  </div>
                  <p className="font-serif text-sm leading-5 text-muted-foreground">{text}</p>
                </li>
              ))}
            </ol>
          </div>
        </section>
      </main>
      <SiteFooter />
    </div>
  );
}
