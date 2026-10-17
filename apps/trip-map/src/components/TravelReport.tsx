import { useEffect, useMemo, useRef, useState } from "react";
import { DownloadSimple, Medal, CalendarBlank, ChartBar } from "@phosphor-icons/react";
import { Button } from "@kongken/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "@kongken/ui/dialog";
import regions from "@kongken/geo-data/regions.json";
import type { Saved } from "../lib/travel";
import { totalCities, percentage, visitedCityCount, visitedTripCount } from "../lib/statistics";
import {
  buildReport,
  detectBrowserTimezone,
  DEFAULT_TIMEZONE,
  type ReportRecordRef,
  type TravelReportData,
} from "../lib/report";
import { reportApi } from "../api/report";

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  saved: Saved;
  province: (typeof regions)[number] | null;
  shapes: { d: string; fill: string; stroke?: string; strokeWidth?: number }[] | null;
  inset: string[];
  cloudSynced?: boolean;
  onInspectRecord?: (provinceCode: string, regionCode: string) => void;
};

type ReportTab = "overview" | "annual" | "achievements";

const TIMEZONE_CHOICES = [
  { value: "Asia/Shanghai", label: "Asia/Shanghai（中国标准时间）" },
  { value: "UTC", label: "UTC（协调世界时）" },
  { value: "America/New_York", label: "America/New_York（美东时间）" },
];

export default function TravelReport({
  open,
  onOpenChange,
  saved,
  province,
  shapes,
  inset,
  cloudSynced = false,
  onInspectRecord,
}: Props) {
  const poster = useRef<SVGSVGElement>(null);
  const [tab, setTab] = useState<ReportTab>("overview");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [imageUrl, setImageUrl] = useState("");
  const browserTz = useMemo(() => detectBrowserTimezone(), []);
  const [timezone, setTimezone] = useState<string>(() => browserTz || DEFAULT_TIMEZONE);
  const [selectedYear, setSelectedYear] = useState<number>(0);
  const [serverReport, setServerReport] = useState<TravelReportData | null>(null);

  useEffect(() => () => { if (imageUrl) URL.revokeObjectURL(imageUrl); }, [imageUrl]);

  const localReport = useMemo(
    () => buildReport(saved, { timezone, year: selectedYear || undefined }),
    [saved, timezone, selectedYear],
  );

  useEffect(() => {
    if (!open || !cloudSynced) {
      setServerReport(null);
      return;
    }
    let cancelled = false;
    reportApi
      .getReport(timezone, selectedYear || undefined)
      .then(res => {
        if (!cancelled) setServerReport(res);
      })
      .catch(() => {
        if (!cancelled) setServerReport(null);
      });
    return () => {
      cancelled = true;
    };
  }, [open, cloudSynced, saved, timezone, selectedYear]);

  const report = useMemo(() => {
    if (
      cloudSynced &&
      serverReport &&
      serverReport.overview.exploredProvinces === localReport.overview.exploredProvinces &&
      serverReport.overview.visitedRegions === localReport.overview.visitedRegions &&
      serverReport.overview.totalTrips === localReport.overview.totalTrips
    ) {
      return serverReport;
    }
    return localReport;
  }, [cloudSynced, serverReport, localReport]);

  const activeYear = selectedYear && report.availableYears.includes(selectedYear) ? selectedYear : report.selectedYear;
  const activeAnnual = report.annualReports.find(r => r.year === activeYear) ?? report.annualReports[0];
  const unlockedCount = report.achievements.filter(a => a.unlocked).length;

  const explored = new Set([...Object.keys(saved.provinces), ...saved.visits.map(v => v.province)]).size;
  const cityCount = visitedCityCount(saved);
  const tripTotal = visitedTripCount(saved);
  const count = province ? saved.visits.filter(v => v.province === province.code).length : cityCount;
  const total = province ? province.cities.length : totalCities;
  const scope = province?.name || "中国";
  const [date] = useState(() => new Date().toLocaleDateString("en-CA"));
  const ranking = regions
    .map(region => ({
      ...region,
      count: saved.visits.filter(v => v.province === region.code).length,
      progress: report.overview.provinceProgress.find(p => p.provinceCode === region.code),
    }))
    .filter(region => region.count > 0)
    .sort((a, b) => b.count / b.cities.length - a.count / a.cities.length || b.count - a.count);

  const tzOptions = useMemo(() => {
    if (TIMEZONE_CHOICES.some(c => c.value === browserTz)) return TIMEZONE_CHOICES;
    return [{ value: browserTz, label: `${browserTz}（当前设备时区）` }, ...TIMEZONE_CHOICES];
  }, [browserTz]);

  async function download() {
    if (!poster.current) return;
    setBusy(true);
    setError("");
    setImageUrl("");
    let source = "";
    try {
      await document.fonts.ready;
      source = URL.createObjectURL(
        new Blob([new XMLSerializer().serializeToString(poster.current)], { type: "image/svg+xml;charset=utf-8" }),
      );
      const image = new Image();
      image.src = source;
      await image.decode();
      const canvas = document.createElement("canvas");
      canvas.width = 1800;
      canvas.height = 2000;
      const context = canvas.getContext("2d");
      if (!context) throw new Error();
      context.drawImage(image, 0, 0, canvas.width, canvas.height);
      const blob = await new Promise<Blob>((resolve, reject) =>
        canvas.toBlob(value => (value ? resolve(value) : reject(new Error())), "image/png"),
      );
      const url = URL.createObjectURL(blob);
      setImageUrl(url);
      const link = document.createElement("a");
      link.href = url;
      link.download = `山海足迹-${scope}-${date}.png`;
      link.click();
    } catch {
      setError("图片生成失败，请重试。");
    } finally {
      if (source) URL.revokeObjectURL(source);
      setBusy(false);
    }
  }

  function inspectRef(ref?: ReportRecordRef) {
    if (!ref || !onInspectRecord || !ref.provinceCode || !ref.regionCode) return;
    onOpenChange(false);
    onInspectRecord(ref.provinceCode, ref.regionCode);
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="report-dialog">
        <DialogHeader>
          <DialogTitle>我的足迹统计</DialogTitle>
          <DialogDescription>把走过的山海，收藏成一张图片，或查看年度报告与旅行成就。</DialogDescription>
        </DialogHeader>
        <div className="report-summary">
          <div>
            <strong>
              {explored} / {regions.length}
            </strong>
            <span>已探索省份 · {percentage(explored, regions.length)}</span>
          </div>
          <div>
            <strong>
              {cityCount} / {totalCities}
            </strong>
            <span>全国城市打卡率 · {percentage(cityCount, totalCities)}</span>
          </div>
          <div>
            <strong>{tripTotal}</strong>
            <span>旅行次数 · 同城多次不增加城市数</span>
          </div>
        </div>
        <p className="report-hint">
          全国城市含自治州及县市；直辖市、香港、澳门各计一座，省内仍按区县统计。省份独立标记不增加城市数。
        </p>

        <div className="report-tabs" role="tablist" aria-label="统计与报告视图">
          <button
            type="button"
            role="tab"
            aria-selected={tab === "overview"}
            className={tab === "overview" ? "active" : ""}
            onClick={() => setTab("overview")}
          >
            <ChartBar size={16} /> 总览与分享图
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={tab === "annual"}
            className={tab === "annual" ? "active" : ""}
            onClick={() => setTab("annual")}
          >
            <CalendarBlank size={16} /> 年度报告
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={tab === "achievements"}
            className={tab === "achievements" ? "active" : ""}
            onClick={() => setTab("achievements")}
          >
            <Medal size={16} /> 旅行成就（{unlockedCount}/{report.achievements.length}）
          </button>
        </div>

        {tab === "overview" && (
          <>
            <div className="report-poster">
              {!shapes ? (
                <p role="status">地图暂不可用，请关闭弹窗后重新加载地图，再生成图片。</p>
              ) : (
                <svg
                  ref={poster}
                  xmlns="http://www.w3.org/2000/svg"
                  viewBox="0 0 900 1000"
                  width="900"
                  height="1000"
                  role="img"
                  aria-label={`${scope}足迹分享图片预览`}
                  style={{ fontFamily: "Arial, 'PingFang SC', 'Microsoft YaHei', sans-serif" }}
                >
                  <rect width="900" height="1000" fill="#f6f7ef" />
                  <rect x="30" y="30" width="840" height="940" rx="24" fill="#fffef9" stroke="#dee5d9" />
                  <text x="70" y="91" fontSize="20" fill="#69816b" letterSpacing="4">
                    山海足迹 / SHANHAI TRAVEL
                  </text>
                  <text x="70" y="155" fontSize="40" fontWeight="600" fill="#355943">
                    {scope} · 我的旅行足迹
                  </text>
                  <text x="70" y="194" fontSize="19" fill="#7b8874">
                    世界很大，慢慢留下足迹。
                  </text>
                  <g transform="translate(25,215)">
                    {shapes.map((shape, i) => (
                      <path
                        key={i}
                        d={shape.d}
                        fill={shape.fill}
                        stroke={shape.stroke || "#fffef9"}
                        strokeWidth={shape.strokeWidth ?? 1.5}
                      />
                    ))}
                    {!province && (
                      <g transform="translate(736,458)">
                        <rect width="83" height="109" rx="4" fill="#fafbf8" stroke="#d6ddd7" />
                        {inset.map((d, i) => (
                          <path key={i} d={d} fill="none" stroke="#b6c5ba" strokeWidth="1" />
                        ))}
                        <text x="41" y="99" textAnchor="middle" fontSize="11" fill="#69816b">
                          南海诸岛
                        </text>
                      </g>
                    )}
                  </g>
                  <line x1="70" x2="830" y1="810" y2="810" stroke="#dee5d9" />
                  {[
                    { value: `${count} / ${total}`, label: province ? "省内打卡区域" : "全国打卡城市" },
                    { value: percentage(count, total), label: province ? "省内打卡率" : "全国城市打卡率" },
                    { value: `${explored} / ${regions.length}`, label: "已探索省份" },
                  ].map((metric, i) => (
                    <g key={metric.label} transform={`translate(${70 + i * 260},860)`}>
                      <text fontSize="34" fill="#355943">
                        {metric.value}
                      </text>
                      <text y="35" fontSize="17" fill="#7b8874">
                        {metric.label}
                      </text>
                    </g>
                  ))}
                  <text x="70" y="937" fontSize="14" fill="#929b89">
                    记录于 {date} · {province ? "省内按地图区域统计" : "直辖市及港澳各计一座城市"}
                  </text>
                </svg>
              )}
            </div>
            <Button onClick={download} disabled={!shapes || busy}>
              <DownloadSimple size={18} />
              {busy ? "正在生成…" : "生成并下载 PNG 图片"}
            </Button>
            {error && (
              <p role="alert" className="report-error">
                {error}
              </p>
            )}
            {imageUrl && (
              <a className="report-image-link" href={imageUrl} target="_blank" rel="noreferrer">
                查看已生成图片（手机可长按保存）
              </a>
            )}
            <div className="report-ranking">
              <h3>省内打卡进度</h3>
              {!ranking.length && <p>还没有城市足迹，标记第一站后即可查看进度。</p>}
              {ranking.map(region => (
                <div key={region.code} className="report-ranking-item">
                  <div className="report-ranking-bar">
                    <span>{region.short}</span>
                    <progress value={region.count} max={region.cities.length} aria-label={`${region.name}打卡进度`} />
                    <span>
                      {region.count}/{region.cities.length} · {percentage(region.count, region.cities.length)}
                    </span>
                  </div>
                  {region.progress && region.progress.visitedRegions.length > 0 && (
                    <div className="report-trace-chips" aria-label={`${region.name}已打卡区域`}>
                      {region.progress.visitedRegions.map(ref => (
                        <button
                          key={ref.regionCode}
                          type="button"
                          className="report-trace-chip"
                          onClick={() => inspectRef(ref)}
                          title={`查看 ${region.name}${ref.regionName} 记录`}
                        >
                          {ref.regionName}
                          {ref.tripCount > 1 ? ` (${ref.tripCount}次)` : ""}
                        </button>
                      ))}
                    </div>
                  )}
                </div>
              ))}
            </div>
          </>
        )}

        {tab === "annual" && (
          <div className="report-section" aria-label="年度报告">
            <div className="report-tz-bar">
              <label className="report-tz-select">
                <span>统计时区</span>
                <select
                  aria-label="统计时区"
                  value={timezone}
                  onChange={e => setTimezone(e.target.value)}
                >
                  {tzOptions.map(opt => (
                    <option key={opt.value} value={opt.value}>
                      {opt.label}
                    </option>
                  ))}
                </select>
              </label>
              <span className="report-tz-meta">
                本地日期 {report.generatedOn} · {serverReport && cloudSynced ? "云端已汇总" : "本机实时汇总"}
              </span>
            </div>

            <div className="report-years" role="group" aria-label="选择报告年份">
              {report.availableYears.map(y => (
                <button
                  key={y}
                  type="button"
                  aria-pressed={activeYear === y}
                  className={activeYear === y ? "active" : ""}
                  onClick={() => setSelectedYear(y)}
                >
                  {y} 年{y === report.currentYear ? "（今年）" : ""}
                </button>
              ))}
            </div>

            {!activeAnnual || activeAnnual.tripCount === 0 ? (
              <div className="report-empty-box" role="status">
                <p>{activeYear} 年暂无填写日期的旅行记录。为旅行添加真实日期后即可生成当年年度报告。</p>
                {report.overview.undatedTrips > 0 && (
                  <p>另有 {report.overview.undatedTrips} 次未填写日期的旅行计入总览统计。</p>
                )}
              </div>
            ) : (
              <>
                <div className="report-annual-metrics">
                  <div>
                    <strong>{activeAnnual.tripCount} 次</strong>
                    <span>
                      当年旅行次数 · 跨越 {activeAnnual.spanDays} 天
                      {activeAnnual.busiestMonth > 0 ? ` · ${activeAnnual.busiestMonth} 月最活跃` : ""}
                    </span>
                  </div>
                  <div>
                    <strong>{activeAnnual.cityCount} 座</strong>
                    <span>当年走过城市（涉及 {activeAnnual.regionCount} 个区域）</span>
                  </div>
                  <div>
                    <strong>{activeAnnual.newCityCount} 座</strong>
                    <span>当年新增城市（首次打卡 {activeAnnual.newRegionCount} 个区域）</span>
                  </div>
                  <div>
                    <strong>{activeAnnual.provinceCount} 个</strong>
                    <span>当年跨越省份（新增 {activeAnnual.newProvinceCount} 个省份）</span>
                  </div>
                </div>

                <div className="report-highlights">
                  {activeAnnual.earliestTrip && activeAnnual.latestTrip && (
                    <div className="report-highlight-card">
                      <h4>年度首站与末站</h4>
                      <p>
                        首站：{activeAnnual.earliestTrip.date} · {activeAnnual.earliestTrip.provinceName}
                        {activeAnnual.earliestTrip.regionName}
                      </p>
                      <p>
                        末站：{activeAnnual.latestTrip.date} · {activeAnnual.latestTrip.provinceName}
                        {activeAnnual.latestTrip.regionName}
                      </p>
                    </div>
                  )}
                  {activeAnnual.longestNoteTrip && (
                    <div className="report-highlight-card">
                      <div className="report-card-head">
                        <h4>最长旅行记忆</h4>
                        {onInspectRecord && (
                          <button
                            type="button"
                            className="report-link-btn"
                            onClick={() => inspectRef(activeAnnual.longestNoteTrip)}
                          >
                            定位记录
                          </button>
                        )}
                      </div>
                      <p>
                        {activeAnnual.longestNoteTrip.date} · {activeAnnual.longestNoteTrip.provinceName}
                        {activeAnnual.longestNoteTrip.regionName}（{activeAnnual.longestNoteTrip.noteLength} 字）
                      </p>
                      <p className="report-quote">
                        {activeAnnual.longestNoteTrip.notePreview || "当年旅行暂未填写文字备注"}
                      </p>
                    </div>
                  )}
                  {activeAnnual.mostVisitedCity && (
                    <div className="report-highlight-card">
                      <div className="report-card-head">
                        <h4>年度常去目的地</h4>
                        {onInspectRecord && (
                          <button
                            type="button"
                            className="report-link-btn"
                            onClick={() => inspectRef(activeAnnual.mostVisitedCity)}
                          >
                            定位记录
                          </button>
                        )}
                      </div>
                      <p>
                        {activeAnnual.mostVisitedCity.provinceName}
                        {activeAnnual.mostVisitedCity.regionName} · 当年到访 {activeAnnual.mostVisitedCity.tripCount} 次
                      </p>
                    </div>
                  )}
                </div>

                {activeAnnual.newRegions.length > 0 && (
                  <div className="report-trace-block">
                    <h4>{activeAnnual.year} 年首次解锁区域（{activeAnnual.newRegions.length}）</h4>
                    <div className="report-trace-chips">
                      {activeAnnual.newRegions.map(ref => (
                        <button
                          key={ref.regionCode}
                          type="button"
                          className="report-trace-chip"
                          onClick={() => inspectRef(ref)}
                        >
                          {ref.provinceName} · {ref.regionName}（{ref.date}）
                        </button>
                      ))}
                    </div>
                  </div>
                )}

                <div className="report-trace-block">
                  <h4>{activeAnnual.year} 年跨越省份（{activeAnnual.provinces.length}）</h4>
                  <div className="report-trace-chips">
                    {activeAnnual.provinces.map(ref => (
                      <span key={ref.provinceCode} className="report-trace-badge">
                        {ref.provinceName} · {ref.tripCount} 次（首访 {ref.date}）
                      </span>
                    ))}
                  </div>
                </div>

                <div className="report-trace-block">
                  <h4>{activeAnnual.year} 年旅行记录追溯（共 {activeAnnual.trips.length} 次）</h4>
                  <ul className="report-trip-list">
                    {activeAnnual.trips.map(tr => (
                      <li key={tr.tripId}>
                        <div className="report-trip-main">
                          <strong>
                            {tr.date} · {tr.provinceName}
                            {tr.regionName}
                          </strong>
                          <span>{tr.notePreview || "未填写备注"}</span>
                        </div>
                        {onInspectRecord && (
                          <button type="button" className="report-link-btn" onClick={() => inspectRef(tr)}>
                            查看记录
                          </button>
                        )}
                      </li>
                    ))}
                  </ul>
                </div>
              </>
            )}
          </div>
        )}

        {tab === "achievements" && (
          <div className="report-section" aria-label="旅行成就">
            <p className="report-ach-summary">
              已解锁 <strong>{unlockedCount}</strong> / {report.achievements.length} 项成就 · 每项成就均可追溯到对应足迹记录
            </p>
            <div className="report-ach-list">
              {report.achievements.map(ach => (
                <div key={ach.id} className={`report-ach-card ${ach.unlocked ? "unlocked" : "locked"}`}>
                  <div className="report-ach-head">
                    <div>
                      <h4>{ach.title}</h4>
                      <p>{ach.description}</p>
                    </div>
                    <span className="report-ach-status">
                      {ach.unlocked ? "已解锁" : "未解锁"} · {ach.current}/{ach.target}
                    </span>
                  </div>
                  <progress
                    value={Math.min(ach.current, ach.target)}
                    max={ach.target}
                    aria-label={`${ach.title}进度`}
                  />
                  {ach.unlocked && ach.unlockedAt && (
                    <span className="report-ach-date">解锁日期：{ach.unlockedAt}</span>
                  )}
                  {ach.evidence.length > 0 && (
                    <div className="report-trace-chips" aria-label={`${ach.title}关联记录`}>
                      {ach.evidence.map((ev, i) => {
                        const label =
                          ev.kind === "province"
                            ? `${ev.provinceName}${ev.date ? ` (${ev.date})` : ""}`
                            : `${ev.regionName}${ev.date ? ` · ${ev.date}` : ""}${ev.notePreview ? ` · ${ev.notePreview}` : ""}`;
                        return ev.regionCode && onInspectRecord ? (
                          <button
                            key={`${ach.id}-${ev.kind}-${ev.provinceCode}-${ev.regionCode}-${ev.tripId}-${i}`}
                            type="button"
                            className="report-trace-chip"
                            onClick={() => inspectRef(ev)}
                          >
                            {label}
                          </button>
                        ) : (
                          <span
                            key={`${ach.id}-${ev.kind}-${ev.provinceCode}-${i}`}
                            className="report-trace-badge"
                          >
                            {label}
                          </span>
                        );
                      })}
                    </div>
                  )}
                </div>
              ))}
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
