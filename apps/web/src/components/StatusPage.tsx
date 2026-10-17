import { useEffect, useState } from "react";
import { Compass } from "@phosphor-icons/react";
import { Button } from "@trip-map/ui/button";
import { getBackendStatus, statusApi } from "@/api/system";
import { dependencyLabel } from "@/lib/status";
import "./StatusPage.css";

type Dependency = { name: string; ready: boolean };
type State =
  | { kind: "loading" }
  | { kind: "ready"; ready: boolean; dependencies: Dependency[]; shareEnabled: boolean }
  | { kind: "unavailable" }
  | { kind: "off" };

/** Anonymous service status: no sign-in, no local footprints, nothing user-specific. */
export default function StatusPage() {
  const [state, setState] = useState<State>(statusApi.enabled ? { kind: "loading" } : { kind: "off" });
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    getBackendStatus(controller.signal)
      ?.then((s) =>
        setState({ kind: "ready", ready: s.ready, shareEnabled: s.shareEnabled, dependencies: s.dependencies.map((d) => ({ name: d.name, ready: d.ready })) }),
      )
      .catch(() => {
        if (!controller.signal.aborted) setState({ kind: "unavailable" });
      });
    return () => controller.abort();
  }, [attempt]);

  const recheck = () => {
    setState({ kind: "loading" });
    setAttempt((n) => n + 1);
  };

  return (
    <main className="status-page">
      <header className="status-brand">
        <span className="status-brand-icon">
          <Compass size={22} weight="duotone" />
        </span>
        <span>山海足迹</span>
      </header>
      <section className="status-card" aria-live="polite">
        <h1>服务状态</h1>
        {state.kind === "loading" && <p className="status-note">正在检查…</p>}
        {state.kind === "off" && <p className="status-note">当前为本地模式，没有连接云端服务。</p>}
        {state.kind === "unavailable" && (
          <>
            <p className="status-summary status-bad">暂时无法获取服务状态</p>
            <p className="status-note">云端服务可能正在维护或网络不稳定，本地足迹不受影响。</p>
          </>
        )}
        {state.kind === "ready" && (
          <>
            <p className={`status-summary ${state.ready ? "status-good" : "status-bad"}`}>{state.ready ? "全部服务正常" : "部分服务异常"}</p>
            <ul className="status-list">
              {state.dependencies.map((d) => (
                <li key={d.name}>
                  <span>{dependencyLabel(d.name)}</span>
                  <span className={d.ready ? "status-good" : "status-bad"}>{d.ready ? "正常" : "异常"}</span>
                </li>
              ))}
              <li>
                <span>分享链接</span>
                <span className="status-muted">{state.shareEnabled ? "已开启" : "未开启"}</span>
              </li>
            </ul>
          </>
        )}
        {state.kind !== "off" && (
          <Button type="button" variant="outline" disabled={state.kind === "loading"} onClick={recheck}>
            重新检查
          </Button>
        )}
      </section>
      <a className="status-back" href={import.meta.env.BASE_URL}>
        返回地图
      </a>
    </main>
  );
}
