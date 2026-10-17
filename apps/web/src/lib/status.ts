/** True for the public status page, `<base>status`. */
export function isStatusPath(pathname: string, base = "/"): boolean {
  const prefix = base.endsWith("/") ? base : `${base}/`;
  const rest = pathname.startsWith(prefix) ? pathname.slice(prefix.length) : "";
  return rest === "status" || rest === "status/";
}

const LABELS: Record<string, string> = { postgres: "数据库", redis: "缓存", s3: "对象存储", tripmap: "旅行服务" };

/** Display name of a dependency; unknown names are shown as reported. */
export const dependencyLabel = (name: string) => LABELS[name] ?? name;
