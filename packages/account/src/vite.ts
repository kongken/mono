import { landingKind } from "./paths.ts";

type Req = { url?: string };
type Res = { setHeader(name: string, value: string): void; writeHead: (...args: any[]) => unknown };
type Next = () => void;
type Middlewares = { use(handler: (req: Req, res: Res, next: Next) => void): unknown };

function handler(base: string) {
  return (req: Req, res: Res, next: Next) => {
    const path = (req.url ?? "").split("?")[0] ?? "";
    if (landingKind(path, base)) {
      // The same headers the production static server sends for these pages.
      res.setHeader("Referrer-Policy", "no-referrer");
      res.setHeader("Cache-Control", "no-store");
      // Vite's own HTML and static middlewares set Cache-Control later, in two different ways;
      // for these pages the stricter value always wins.
      const setHeader = res.setHeader.bind(res);
      res.setHeader = (name: string, value: string) => setHeader(name, name.toLowerCase() === "cache-control" ? "no-store" : value);
      const writeHead = res.writeHead.bind(res);
      res.writeHead = (...args: any[]) => {
        const headers = args.find((a) => a && typeof a === "object" && !Array.isArray(a));
        if (headers) for (const name of Object.keys(headers)) if (name.toLowerCase() === "cache-control") headers[name] = "no-store";
        return writeHead(...args);
      };
    }
    next();
  };
}

/**
 * Vite plugin: serves the mail landing pages (`/account/verify-email`,
 * `/account/reset-password`) with `Referrer-Policy: no-referrer` and `Cache-Control: no-store`
 * in `vite dev` and `vite preview`, like the production nginx does.
 */
export function accountLandingHeaders(base = "/") {
  return {
    name: "kongken-account-landing-headers",
    configureServer(server: { middlewares: Middlewares }) {
      server.middlewares.use(handler(base));
    },
    configurePreviewServer(server: { middlewares: Middlewares }) {
      server.middlewares.use(handler(base));
    },
  };
}
