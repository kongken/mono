import { useState } from "react";
import { Check, Plus, PencilSimple, MagnifyingGlass } from "@phosphor-icons/react";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "./ui/dialog";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import type { Visit } from "../lib/travel";

type Props = {
  name: string;
  cities: { code: string; name: string }[];
  visits: Visit[];
  onClose: () => void;
  onToggle: (code: string) => void;
  onEdit: (code: string) => void;
};

export default function CityPicker({ name, cities, visits, onClose, onToggle, onEdit }: Props) {
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState("all");
  const visited = new Map(visits.map(visit => [visit.code, visit]));
  const filtered = cities.filter(city => city.name.includes(search.trim()) &&
    (filter === "all" || visited.has(city.code) === (filter === "visited")));
  return <Dialog open onOpenChange={open => { if (!open) onClose(); }}>
    <DialogContent className="city-picker-dialog">
      <DialogHeader>
        <DialogTitle>{name} · 选择城市 / 区县</DialogTitle>
        <DialogDescription>点选标记去过，再次点选取消；可连续选择多座城市。</DialogDescription>
      </DialogHeader>
      <div className="city-picker-search"><MagnifyingGlass size={18} /><Input aria-label="查找城市或区县" placeholder="搜索城市或区县名称" value={search} onChange={event => setSearch(event.target.value)} /></div>
      <div className="city-picker-filters" role="group" aria-label="筛选打卡状态">
        {[{ value: "all", label: "全部" }, { value: "unvisited", label: "未打卡" }, { value: "visited", label: "已打卡" }].map(option =>
          <Button key={option.value} size="sm" variant={filter === option.value ? "default" : "outline"} aria-pressed={filter === option.value} onClick={() => setFilter(option.value)}>{option.label}</Button>)}
      </div>
      <p className="city-picker-count" role="status">已打卡 {cities.filter(city => visited.has(city.code)).length} / {cities.length} · 当前显示 {filtered.length} 个区域</p>
      <div className="city-picker-list">
        {filtered.map(city => {
          const visit = visited.get(city.code);
          return <div key={city.code} className={`city-row ${visit ? "city-visited" : ""}`}>
            <button className="province-row city-toggle" aria-pressed={!!visit} aria-label={`${city.name}，${visit ? "取消去过" : "标记去过"}`} onClick={() => onToggle(city.code)}>
              <span className={`province-dot ${visit ? "has-visit" : ""}`} style={visit ? { background: visit.color } : {}} />
              <span>{city.name}</span><small>{visit ? "已去过" : "标记去过"}</small>{visit ? <Check size={18} /> : <Plus size={18} />}
            </button>
            {visit && <button className="city-edit" aria-label={`编辑${city.name}足迹`} onClick={() => onEdit(city.code)}><PencilSimple size={18} /></button>}
          </div>;
        })}
        {!filtered.length && <p className="no-results">没有匹配的区域，试试其他名称或打卡状态。</p>}
      </div>
      <Button variant="outline" onClick={onClose}>完成选择</Button>
    </DialogContent>
  </Dialog>;
}
