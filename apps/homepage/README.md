# Oz homepage

The standalone Oz Digital Studio homepage lives in `apps/homepage`. It has no account or API runtime dependency, so it can be deployed independently as a Cloudflare Pages project.

Pages (Vite multi-page build):

- `/` — studio homepage: product cards for 山海足迹 and 地铁足迹, the OzID block, and the design notes. Entry `index.html`.
- `/extensions/` — Chrome Web Store listings from Oz Studio. Entry `extensions/index.html`, source `src/Extensions.tsx`, list data in `src/extensions.ts`. See [功能描述](../../docs/homepage/features.md).

Both pages share the header/footer in `src/site-chrome.tsx`; icons and store screenshots live in `public/extensions/` and are served locally, so the page makes no third-party requests.

Build locally from the repository root:

```sh
npm ci
npm run build:homepage
```

Dev server: `npm run dev:homepage` (port 5177; `/extensions/` is served by the same Vite server).

The production domain mapping is:

- 山海足迹: `https://tripmap.orz.ee`
- 地铁足迹: `https://metro.orz.ee`
- OzID: `https://id.orz.ee`

The Cloudflare Pages entry point is `wrangler.json`, which uses `apps/homepage/dist` as the Pages output directory. In the Cloudflare Pages dashboard, use:

- Build command: `npm run build:homepage`
- Build output directory: `apps/homepage/dist`
- Root directory: repository root

The deployment serves the public Oz homepage at its own Cloudflare project/domain. The product links point to the exact public domains above, while `OzID` links to `https://id.orz.ee` and remains the separate account center app at `apps/ozid`. The plugin list needs no redirect rule: Cloudflare Pages and the Nginx image both resolve `/extensions/` to `dist/extensions/index.html`.
