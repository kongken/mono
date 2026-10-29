# Oz homepage

The standalone Oz Digital Studio homepage lives in `apps/homepage`. It has no account or API runtime dependency, so it can be deployed independently as a Cloudflare Pages project.

Build locally from the repository root:

```sh
npm ci
npm run build:homepage
```

The production domain mapping is:

- 山海足迹: `https://tripmap.orz.ee`
- 地铁足迹: `https://metro.orz.ee`
- OzID: `https://id.orz.ee`

The Cloudflare Pages entry point is `wrangler.json`, which uses `apps/homepage/dist` as the Pages output directory. In the Cloudflare Pages dashboard, use:

- Build command: `npm run build:homepage`
- Build output directory: `apps/homepage/dist`
- Root directory: repository root

The deployment serves the public Oz homepage at its own Cloudflare project/domain. The product links point to the exact public domains above, while `OzID` links to `https://id.orz.ee` and remains the separate account center app at `apps/ozid`.
