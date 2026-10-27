import { useState, type ComponentType } from "react";
import { ArrowUpRight, Mountains, Train, type IconProps } from "@phosphor-icons/react";
import { Button } from "@kongken/ui/button";
import { navQuery } from "@kongken/account/navigation";
import { useTarget } from "@/lib/target";

function ProductEntry({ appId, title, description, icon: Icon }: {
  appId: string; title: string; description: string; icon: ComponentType<IconProps>;
}) {
  const [attempt, setAttempt] = useState(0);
  const target = useTarget(navQuery({ appId }), attempt);
  return (
    <li className="grid gap-5 rounded-xl bg-card p-6 shadow-sm">
      <div className="flex items-start gap-3">
        <span className="grid size-10 shrink-0 place-items-center rounded-lg bg-muted text-primary"><Icon className="size-6" aria-hidden /></span>
        <div className="grid gap-1">
          <h3 className="font-semibold">{title}</h3>
          <p className="text-sm text-muted-foreground">{description}</p>
        </div>
      </div>
      <div className="flex min-h-9 flex-wrap items-center justify-between gap-3">
        {target.status === "ok" ? (
          <Button asChild variant="outline"><a href={target.target.returnUrl}>打开{title}<ArrowUpRight aria-hidden /></a></Button>
        ) : target.status === "refused" ? (
          <p className="text-sm text-muted-foreground">当前未提供此产品入口。</p>
        ) : target.status === "unavailable" ? (
          <>
            <p role="status" className="text-sm text-muted-foreground">暂时无法读取产品入口。</p>
            <Button variant="outline" onClick={() => setAttempt((n) => n + 1)} aria-label={`重试读取${title}入口`}>重试</Button>
          </>
        ) : <p role="status" className="text-sm text-muted-foreground">正在读取入口…</p>}
      </div>
    </li>
  );
}

export function ProductLinks() {
  return (
    <section className="grid gap-4" aria-labelledby="overview-products">
      <div className="grid gap-1">
        <h2 id="overview-products" className="text-lg font-semibold">接入产品</h2>
        <p className="text-sm text-muted-foreground">使用同一个 OzID 账号，各产品的数据和设置独立管理。</p>
      </div>
      <ul className="grid list-none gap-4 p-0 sm:grid-cols-2">
        <ProductEntry appId="trip-map" title="山海足迹" description="旅行记录与足迹探索" icon={Mountains} />
        <ProductEntry appId="metro" title="地铁足迹" description="城市轨道交通与乘车记录" icon={Train} />
      </ul>
    </section>
  );
}
