import { useEffect, useRef, useState } from "react";
import { DownloadSimple } from "@phosphor-icons/react";
import { Button } from "./ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "./ui/dialog";
import regions from "../regions.json";
import type { Saved } from "../lib/travel";

import { totalCities, percentage } from "../lib/statistics";

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  saved: Saved;
  province: (typeof regions)[number] | null;
  shapes: { d: string; fill: string }[] | null;
  inset: string[];
};

export default function TravelReport({ open, onOpenChange, saved, province, shapes, inset }: Props) {
  const poster = useRef<SVGSVGElement>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [imageUrl, setImageUrl] = useState("");
  useEffect(() => () => { if (imageUrl) URL.revokeObjectURL(imageUrl); }, [imageUrl]);
  const explored = new Set([...Object.keys(saved.provinces), ...saved.visits.map(v => v.province)]).size;
  const count = province ? saved.visits.filter(v => v.province === province.code).length : saved.visits.length;
  const total = province ? province.cities.length : totalCities;
  const scope = province?.name || "中国";
  const [date] = useState(() => new Date().toLocaleDateString("en-CA"));
  const ranking = regions.map(region => ({ ...region, count: saved.visits.filter(v => v.province === region.code).length }))
    .filter(region => region.count > 0)
    .sort((a, b) => b.count / b.cities.length - a.count / a.cities.length || b.count - a.count);

  async function download() {
    if (!poster.current) return;
    setBusy(true);
    setError("");
    setImageUrl("");
    let source = "";
    try {
      await document.fonts.ready;
      source = URL.createObjectURL(new Blob([new XMLSerializer().serializeToString(poster.current)], { type: "image/svg+xml;charset=utf-8" }));
      const image = new Image();
      image.src = source;
      await image.decode();
      const canvas = document.createElement("canvas");
      canvas.width = 1800;
      canvas.height = 2000;
      const context = canvas.getContext("2d");
      if (!context) throw new Error();
      context.drawImage(image, 0, 0, canvas.width, canvas.height);
      const blob = await new Promise<Blob>((resolve, reject) => canvas.toBlob(value => value ? resolve(value) : reject(new Error()), "image/png"));
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

  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent className="report-dialog">
      <DialogHeader>
        <DialogTitle>我的足迹统计</DialogTitle>
        <DialogDescription>把走过的山海，收藏成一张图片。</DialogDescription>
      </DialogHeader>
      <div className="report-summary">
        <div><strong>{explored} / {regions.length}</strong><span>已探索省份 · {percentage(explored, regions.length)}</span></div>
        <div><strong>{saved.visits.length} / {totalCities}</strong><span>全国城市打卡率 · {percentage(saved.visits.length, totalCities)}</span></div>
      </div>
      <p className="report-hint">城市含自治州、县市及区县；省份独立标记仅计入已探索省份。打卡率按当前地图数据计算。</p>
      <div className="report-poster">
        {!shapes ? <p role="status">地图暂不可用，请关闭弹窗后重新加载地图，再生成图片。</p> :
          <svg ref={poster} xmlns="http://www.w3.org/2000/svg" viewBox="0 0 900 1000" width="900" height="1000" role="img" aria-label={`${scope}足迹分享图片预览`}
            style={{ fontFamily: "Arial, 'PingFang SC', 'Microsoft YaHei', sans-serif" }}>
            <rect width="900" height="1000" fill="#f6f7ef" />
            <rect x="30" y="30" width="840" height="940" rx="24" fill="#fffef9" stroke="#dee5d9" />
            <text x="70" y="91" fontSize="20" fill="#69816b" letterSpacing="4">山海足迹 / SHANHAI TRAVEL</text>
            <text x="70" y="155" fontSize="40" fontWeight="600" fill="#355943">{scope} · 我的旅行足迹</text>
            <text x="70" y="194" fontSize="19" fill="#7b8874">世界很大，慢慢留下足迹。</text>
            <g transform="translate(25,215)">
              {shapes.map((shape, i) => <path key={i} d={shape.d} fill={shape.fill} stroke="#fffef9" strokeWidth="1.5" />)}
              {!province && <g transform="translate(736,458)">
                <rect width="83" height="109" rx="4" fill="#fafbf8" stroke="#d6ddd7" />
                {inset.map((d, i) => <path key={i} d={d} fill="none" stroke="#b6c5ba" strokeWidth="1" />)}
                <text x="41" y="99" textAnchor="middle" fontSize="11" fill="#69816b">南海诸岛</text>
              </g>}
            </g>
            <line x1="70" x2="830" y1="810" y2="810" stroke="#dee5d9" />
            {[{ value: `${count} / ${total}`, label: province ? "省内打卡区域" : "全国打卡区域" },
              { value: percentage(count, total), label: province ? "省内打卡率" : "全国城市打卡率" },
              { value: `${explored} / ${regions.length}`, label: "已探索省份" }].map((metric, i) => <g key={metric.label} transform={`translate(${70 + i * 260},860)`}>
                <text fontSize="34" fill="#355943">{metric.value}</text>
                <text y="35" fontSize="17" fill="#7b8874">{metric.label}</text>
              </g>)}
            <text x="70" y="937" fontSize="14" fill="#929b89">记录于 {date} · 城市含自治州、县市及区县</text>
          </svg>}
      </div>
      <Button onClick={download} disabled={!shapes || busy}><DownloadSimple size={18} />{busy ? "正在生成…" : "生成并下载 PNG 图片"}</Button>
      {error && <p role="alert" className="report-error">{error}</p>}
      {imageUrl && <a className="report-image-link" href={imageUrl} target="_blank" rel="noreferrer">查看已生成图片（手机可长按保存）</a>}
      <div className="report-ranking">
        <h3>省内打卡进度</h3>
        {!ranking.length && <p>还没有城市足迹，标记第一站后即可查看进度。</p>}
        {ranking.map(region => <div key={region.code}>
          <span>{region.short}</span><progress value={region.count} max={region.cities.length} aria-label={`${region.name}打卡进度`} />
          <span>{region.count}/{region.cities.length} · {percentage(region.count, region.cities.length)}</span>
        </div>)}
      </div>
    </DialogContent>
  </Dialog>;
}
