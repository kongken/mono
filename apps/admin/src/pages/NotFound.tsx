import { Link } from "@/lib/router";

export default function NotFound() {
  return (
    <section className="grid max-w-md gap-3" data-testid="not-found">
      <h1 className="text-xl font-semibold">没有这个页面</h1>
      <Link to="/" className="text-sm text-primary underline-offset-4 hover:underline">回到控制台首页</Link>
    </section>
  );
}
