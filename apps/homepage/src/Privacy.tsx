import { useEffect, type ReactNode } from "react";
import { ArrowLeft, ArrowUpRight, Fingerprint, PuzzlePiece } from "@phosphor-icons/react";
import { SiteFooter, SiteHeader, focus } from "./site-chrome";

const lastUpdated = "2026-11-07";

const sections = [
  { id: "scope", label: "适用范围" },
  { id: "principles", label: "我们的原则" },
  { id: "site", label: "本网站" },
  { id: "account", label: "账号与 OzID" },
  { id: "footprints", label: "足迹与分享" },
  { id: "extensions", label: "Chrome 插件" },
  { id: "third-party", label: "第三方服务" },
  { id: "retention", label: "保存与删除" },
  { id: "rights", label: "你的权利" },
  { id: "children", label: "儿童" },
  { id: "changes", label: "政策更新" },
  { id: "contact", label: "联系我们" },
];

const summary = [
  "没有广告、没有第三方分析或追踪脚本，也不出售或出租数据。",
  "能在浏览器里完成的就留在浏览器：不登录时足迹与插件数据只存在本机。",
  "数据可自行导出；注销账号会在服务端删除账号与全部云端数据。",
];

const thirdParties = [
  ["站点托管与反向代理", "Cloudflare Pages 或 Nginx 镜像", "打开本站或产品页面时，按常规处理访问日志：IP 地址、时间、请求路径与 User-Agent。"],
  ["事务邮件服务", "由部署方选用的邮件厂商", "注册验证、找回密码与安全通知；处理收件邮箱、邮件内容与发送结果。"],
  ["Bing", "OzTab 每日壁纸", "请求当天的壁纸图片，不携带账号信息。"],
  ["Open-Meteo", "OzTab 天气与城市搜索", "城市名称或坐标，用于取天气与地理编码结果。"],
  ["Google s2 图标服务", "OzTab 快捷方式图标", "快捷方式指向的站点域名。"],
  ["Linear", "OzTab 的可选议题接入", "由你自己提供的 API 密钥与议题查询，只在你的浏览器与 Linear 之间传递。"],
  ["Chrome 网上应用店", "插件安装与更新", "由 Google 按其隐私政策处理，我们只能看到商店提供的汇总安装数据。"],
];

function Section({ id, index, title, children }: { id: string; index: string; title: string; children: ReactNode }) {
  return (
    <section id={id} aria-labelledby={`${id}-title`} className="scroll-mt-24 border-t border-border/50 pt-8">
      <div className="mb-1 font-mono text-[11px] uppercase tracking-[.12em] text-[var(--secondary)]">{index}</div>
      <h2 id={`${id}-title`} className="mb-3 font-serif text-[24px] leading-8 tracking-tight">{title}</h2>
      <div className="grid gap-3 text-[15px] leading-6 text-muted-foreground">{children}</div>
    </section>
  );
}

function Items({ children }: { children: ReactNode }) {
  return <ul className="grid gap-2 pl-5 marker:text-[var(--secondary)] [&>li]:list-disc">{children}</ul>;
}

export default function PrivacyPage() {
  useEffect(() => { document.title = "隐私政策 · Oz Studio"; }, []);
  // Sections render after mount, so a deep link to #extensions would otherwise land at the top.
  useEffect(() => {
    const id = window.location.hash.slice(1);
    if (!id) return;
    document.getElementById(id)?.scrollIntoView({ block: "start" });
  }, []);
  return (
    <div className="min-h-screen bg-[var(--surface)] text-foreground selection:bg-[var(--secondary-container)]">
      <SiteHeader current="privacy" />
      <main id="top" className="pt-16">
        <section className="mx-auto max-w-[1280px] px-5 pb-14 pt-12 md:px-12 md:pt-20">
          <a href="/" className={`${focus} inline-flex items-center gap-1.5 font-mono text-[11px] tracking-[.04em] text-muted-foreground hover:text-foreground`}>
            <ArrowLeft size={14} aria-hidden />返回首页
          </a>
          <div className="mt-7 mb-3 font-mono text-[11px] uppercase tracking-[.12em] text-[var(--secondary)]">Privacy / 隐私政策</div>
          <div className="grid items-end gap-8 lg:grid-cols-12">
            <div className="lg:col-span-7">
              <h1 className="mb-5 max-w-[620px] font-serif text-[clamp(38px,5vw,60px)] leading-[1.1] tracking-[-.02em]">我们说清楚收集什么，也说明白不做什么。</h1>
              <p className="max-w-[540px] text-lg leading-7 text-muted-foreground">这份政策覆盖 Oz Studio 的站点、两个足迹 Web 产品、OzID 账号中心，以及 Chrome 网上应用店上的两个插件。每一项都按「收集什么、存在哪里、怎么删除」写。</p>
            </div>
            <div className="flex flex-wrap items-center gap-2 lg:col-span-5 lg:justify-end">
              {[`最后更新 ${lastUpdated}`, "中文", "无广告 · 无追踪"].map((item) => (
                <span key={item} className="rounded-full bg-[var(--surface-container-high)] px-3 py-1.5 font-mono text-[11px] tracking-[.04em] text-muted-foreground">{item}</span>
              ))}
            </div>
          </div>
        </section>

        <section aria-label="要点" className="mx-auto max-w-[1280px] px-5 pb-16 md:px-12">
          <div className="grid gap-4 rounded-xl bg-[var(--surface-low)] p-6 md:grid-cols-3 md:p-8">
            {summary.map((item, index) => (
              <div key={item} className="flex gap-3">
                <span className="mt-0.5 font-mono text-[11px] text-[var(--secondary)]">0{index + 1}</span>
                <p className="text-[14px] leading-6 text-muted-foreground">{item}</p>
              </div>
            ))}
          </div>
        </section>

        <div className="mx-auto max-w-[1280px] px-5 pb-24 md:px-12">
          <div className="grid gap-10 lg:grid-cols-12 lg:gap-12">
            <aside className="hidden lg:col-span-3 lg:block">
              <nav aria-label="本页目录" className="sticky top-24 grid gap-2 font-mono text-[11px] tracking-[.04em] text-muted-foreground">
                {sections.map((section, index) => (
                  <a key={section.id} href={`#${section.id}`} className={`${focus} hover:text-foreground`}>
                    <span className="mr-2 text-[var(--secondary)]">{String(index + 1).padStart(2, "0")}</span>
                    {section.label}
                  </a>
                ))}
              </nav>
            </aside>

            <div className="grid gap-8 lg:col-span-9 lg:max-w-[760px]">
              <Section id="scope" index="01 / Scope" title="适用范围">
                <p>本政策说明 Oz Studio（在 Chrome 网上应用店署名 OzLabs）在下列产品中如何处理个人信息：</p>
                <Items>
                  <li>本站：工作室首页、Chrome 插件列表与本页。</li>
                  <li>OzID：山海足迹与地铁足迹共用的账号中心，负责登录、资料、密码、登录设备与注销。</li>
                  <li>山海足迹：记录中国旅行地图的 Web 应用；地铁足迹：记录乘坐过的线路与车站，含公开主页。</li>
                  <li>Chrome 插件：RequestLoom — Headers &amp; Redirects 与 OzTab。</li>
                </Items>
                <p>各产品的功能以对应页面为准；某个产品如果有更具体的说明，以那个说明为准。</p>
              </Section>

              <Section id="principles" index="02 / Principles" title="我们的原则">
                <Items>
                  <li>只收集功能必需的信息，不做用户画像，不出售、不出租，也不与广告商分享数据。</li>
                  <li>能在浏览器里完成的计算与存储，就不发送到服务器。</li>
                  <li>每一项数据都能自己导出、自己删除，不需要写邮件申请。</li>
                  <li>不引入第三方广告、分析、崩溃上报或 A/B 测试脚本。</li>
                </Items>
              </Section>

              <Section id="site" index="03 / This site" title="本网站">
                <Items>
                  <li>首页、插件列表与本页都是静态页面：没有 Cookie，没有登录，没有表单，也没有统计或分析脚本。</li>
                  <li>图标、截图与字体随构建一起发布，页面本身不向第三方发起请求；只有你点击指向外部的链接之后，才由对方站点按其政策处理。</li>
                  <li>站点由静态托管与反向代理提供，托管方会按常规记录访问日志（IP 地址、时间、请求路径、User-Agent），只用于安全防护与故障排查。</li>
                </Items>
              </Section>

              <Section id="account" index="04 / Account" title="账号与 OzID">
                <p>山海足迹与地铁足迹共用一个账号，账号页面全部在 OzID。</p>
                <Items>
                  <li>用邮箱加密码登录。密码只保存 Argon2id 哈希，不保存明文；登录邮箱只在 OzID 账号界面显示给本人。</li>
                  <li>与账号一起保存的还有昵称、文字头像、角色、账号状态，以及登录设备（会话）与安全审计记录。审计只记录用户 UUID、动作与时间，不记录邮箱、密码或令牌。</li>
                  <li>登录靠 API 主机上的 HttpOnly 会话 Cookie，用于保持登录与防跨站请求伪造；它不用于广告，也不做跨站追踪。</li>
                  <li>注册验证、找回密码与安全通知通过事务邮件服务发送；排队中的收件地址与令牌加密保存，发出后即清除，任务记录保留 7 天。</li>
                  <li>如果部署启用了 Google 或 GitHub 登录，我们只读取该服务返回的账号标识与已验证邮箱，用于登录或绑定账号；不读取联系人、邮件内容或其他资料。</li>
                  <li>退出登录只结束当前浏览器的会话；修改密码会让所有设备上的登录同时失效。</li>
                </Items>
              </Section>

              <Section id="footprints" index="05 / Footprints" title="足迹与分享">
                <Items>
                  <li>未登录时（本地模式），两个产品的足迹、笔记与照片只保存在浏览器的本地存储里，不上传；清除站点数据即删除，服务器没有副本。</li>
                  <li>登录后，足迹、旅行记录、同步快照与照片附件保存在 Oz Studio 自己的服务器与对象存储中，并按账号隔离：每次读取与写入都校验账号身份，其他账号看不到。共同旅行的成员只能看到被邀请的那次旅行。</li>
                  <li>公开分享是你主动发布的快照，不是实时页面：发布时保存当时的内容，之后新增的足迹与照片不会自动公开，需要再次预览并确认。分享默认「知道链接的人可查看」；城市名称、日期、备注与照片分别有开关且默认关闭，只有打开的内容才会出现在公开页面。</li>
                  <li>地铁足迹的公开主页（显示名称、handle、头图与社交链接）默认私有，只有设置 handle 并打开公开开关后，公开链接才可访问。</li>
                  <li>你可以在产品里随时导出 JSON 备份与原始数据。</li>
                </Items>
              </Section>

              <Section id="extensions" index="06 / Extensions" title="Chrome 插件">
                <div className="grid gap-4">
                  <div className="rounded-lg bg-[var(--surface-low)] p-5">
                    <div className="mb-2 flex items-center gap-2"><PuzzlePiece size={16} className="text-primary" aria-hidden /><h3 className="text-[15px] font-medium text-foreground">RequestLoom — Headers &amp; Redirects</h3></div>
                    <Items>
                      <li>档案、规则与偏好保存在 chrome.storage.sync；按你的 Chrome 设置，它可能随 Google 账号同步，但不经过 OzLabs 的服务器。</li>
                      <li>请求头改写与域名重定向由 Chrome 的 declarativeNetRequest 在本机执行；插件没有开发者后端，也没有广告、分析或遥测。</li>
                      <li>导出的配置 JSON 含有你填写的内容（包括头部值），分享前请自行检查。</li>
                    </Items>
                  </div>
                  <div className="rounded-lg bg-[var(--surface-low)] p-5">
                    <div className="mb-2 flex items-center gap-2"><PuzzlePiece size={16} className="text-primary" aria-hidden /><h3 className="text-[15px] font-medium text-foreground">OzTab</h3></div>
                    <Items>
                      <li>设置、快捷方式、任务与壁纸缓存保存在浏览器本地（chrome.storage.local 与 IndexedDB），不上传。没有 OzTab 账号，也没有开发者后端。</li>
                      <li>为了显示内容，新标签页会直接向第三方请求：Bing 的每日壁纸、Open-Meteo 的天气与城市搜索、Google 的快捷方式图标。请求只带必要参数（例如城市坐标或目标域名），各自受对应服务的隐私政策约束。</li>
                      <li>Linear 是可选项：用你自己的个人 API 密钥连接（建议只授予读取权限），密钥保存在本机并直接发往 api.linear.app；断开连接会删除保存的连接数据。</li>
                    </Items>
                  </div>
                </div>
                <p>两个插件都不收集浏览历史，也不读取或上传你访问的页面内容。</p>
              </Section>

              <Section id="third-party" index="07 / Third parties" title="第三方服务">
                <p>下面的服务会被实际调用，各自只拿到完成那件事所需的最少信息：</p>
                <div className="grid gap-2">
                  {thirdParties.map(([name, usage, detail]) => (
                    <div key={name} className="rounded-lg bg-[var(--surface-lowest)] p-4">
                      <div className="mb-1 flex flex-wrap items-baseline gap-x-3 gap-y-1">
                        <span className="text-[14px] font-medium text-foreground">{name}</span>
                        <span className="font-mono text-[11px] tracking-[.04em] text-[var(--secondary)]">{usage}</span>
                      </div>
                      <p className="text-[13px] leading-5">{detail}</p>
                    </div>
                  ))}
                </div>
                <p>我们不会把上述任何数据用于广告，也不会出售或出租给第三方。</p>
              </Section>

              <Section id="retention" index="08 / Retention" title="保存与删除">
                <Items>
                  <li>账号数据保留到注销为止。注销会先关闭账号，再删除该账号在各产品的云端足迹、照片与附件、分享与提醒，最后删除账号本身；这些数据不可恢复。</li>
                  <li>注销的回执与清理记录在 24 小时后清除。注销后可以用同一邮箱重新注册，得到一个新账号，不会与旧数据关联。</li>
                  <li>被撤销或已因改密码而失效的会话立即不能使用，相关记录随账号一起删除。</li>
                  <li>邮件队列里的收件地址与令牌在发出后清除，任务记录保留 7 天。</li>
                  <li>本地模式与插件的数据在浏览器里：清除站点数据、删除扩展或卸载浏览器配置即删除。</li>
                </Items>
              </Section>

              <Section id="rights" index="09 / Your choices" title="你的权利">
                <Items>
                  <li>查看与更正：在 OzID 的资料页修改昵称与头像。登录邮箱是登录标识，目前不提供网页修改，需要时请通过下面的方式联系我们。</li>
                  <li>导出：在山海足迹与地铁足迹里导出 JSON 备份与原始数据。</li>
                  <li>删除：在 OzID 注销账号（不可恢复）；只想清掉本机数据时，清除站点数据或删除扩展即可。</li>
                  <li>其他请求，例如访问副本或投诉：通过下面的联系方式提出，我们会在合理时间内答复。</li>
                </Items>
              </Section>

              <Section id="children" index="10 / Children" title="儿童">
                <p>这些工具面向一般用户，不面向 13 岁以下儿童，也不会刻意收集儿童的个人信息。如果你认为我们误收了这类信息，请联系我们删除。</p>
              </Section>

              <Section id="changes" index="11 / Changes" title="政策更新">
                <Items>
                  <li>政策变化时，我们更新本页的「最后更新」日期；如果变化会影响你已有的数据或权利，会在页面顶部单独说明。</li>
                  <li>本页于 {lastUpdated} 首次发布，同时作为各产品与 Chrome 插件列表的隐私政策地址。</li>
                </Items>
              </Section>

              <Section id="contact" index="12 / Contact" title="联系我们">
                <div className="rounded-xl bg-[var(--surface-low)] p-5">
                  <div className="mb-2 flex items-center gap-2"><Fingerprint size={16} className="text-primary" aria-hidden /><h3 className="text-[15px] font-medium text-foreground">隐私相关问题与数据请求</h3></div>
                  <Items>
                    <li>通过 Chrome 网上应用店各插件列表页上的开发者支持联系方式联系 OzLabs，这一类渠道可以私下沟通。</li>
                    <li>也可以在公开仓库 <a className={`${focus} underline decoration-border underline-offset-4 hover:text-foreground`} href="https://github.com/kongken/mono" rel="noopener noreferrer">github.com/kongken/mono</a> 提交 Issue 说明情况；公开 Issue 里请不要填写邮箱、API 密钥或个人数据，我们会回复一个私下沟通的方式。</li>
                  </Items>
                </div>
                <p>数据请求（导出、删除、更正）通常需要先确认账号，所以请从 OzID 里的账号界面或上述开发者渠道发起。</p>
              </Section>
            </div>
          </div>
        </div>

        <section aria-label="相关入口" className="mx-auto max-w-[1280px] px-5 pb-20 md:px-12">
          <div className="flex flex-col items-start justify-between gap-4 rounded-xl bg-[var(--surface-low)] p-6 md:flex-row md:items-center md:p-8">
            <div>
              <div className="mb-1 font-mono text-[11px] uppercase tracking-[.12em] text-[var(--secondary)]">Next / 继续</div>
              <h2 className="font-serif text-xl">想先看看产品，或直接读插件的权限说明？</h2>
            </div>
            <div className="flex flex-wrap items-center gap-3">
              <a href="/" className={`${focus} inline-flex items-center gap-2 rounded bg-[var(--surface-container-highest)] px-4 py-2 font-mono text-[11px] text-foreground hover:bg-[var(--surface-container)]`}><ArrowLeft size={14} aria-hidden />回到首页</a>
              <a href="/extensions/" className={`${focus} inline-flex items-center gap-2 rounded bg-primary px-4 py-2 text-sm text-primary-foreground hover:bg-[var(--primary-container)]`}>查看 Chrome 插件<ArrowUpRight size={16} aria-hidden /></a>
            </div>
          </div>
        </section>
      </main>
      <SiteFooter />
    </div>
  );
}
