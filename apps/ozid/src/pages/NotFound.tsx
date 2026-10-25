import { useEffect } from "react";
import { Link } from "@/lib/router";

export default function NotFound() {
  useEffect(() => {
    document.title = "页面不存在 · OzID";
  }, []);
  return (
    <div className="grid gap-3">
      <h1 className="text-xl font-semibold">页面不存在</h1>
      <p className="text-sm text-muted-foreground">这个地址在 OzID 中没有对应的页面。</p>
      <Link to="/" className="text-sm text-primary underline-offset-4 hover:underline">回到账号概览</Link>
    </div>
  );
}
