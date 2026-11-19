#!/usr/bin/env sh
# Checks the Timeline release examples: the service's own config, the two image contexts, the compose
# stack, the Caddy site and the publish matrix's build arguments. None of them is compiled, so this
# script is their only check (the backend job runs it). It needs nothing but POSIX sh and grep.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
required="config.timeline.example.yaml deploy/timeline/Dockerfile deploy/timeline-web/Dockerfile deploy/timeline-web/default.conf.template deploy/compose.stack.example.yaml deploy/reverse-proxy/Caddyfile.example"
for file in $required; do test -f "$root/$file" || { echo "missing $file" >&2; exit 1; }; done

timeline_config=$root/config.timeline.example.yaml
# The service refuses to start unless db_store names a configured client and every trusted key is a
# file or an inline key: an example that fails Validate() is worse than no example at all.
grep -qx 'db_store: timeline' "$timeline_config"
grep -qx '    timeline:' "$timeline_config"
grep -qF 'public_key_file: dev-secrets/bff.pub' "$timeline_config"
grep -qF 's3_store: timeline' "$timeline_config"
grep -qF 'bucket: timeline' "$timeline_config"

stack=$root/deploy/compose.stack.example.yaml
grep -qF 'image: ghcr.io/kongken/mono-timeline:main' "$stack"
grep -qF 'image: ghcr.io/kongken/mono-timeline-web:main' "$stack"
grep -qF 'test: ["CMD-SHELL", "wget -qO- http://127.0.0.1:9000/health/ready >/dev/null"]' "$stack"
grep -qF 's3-init:' "$stack"
grep -qF 's3-init: { condition: service_completed_successfully }' "$stack"
grep -qF 's3: { condition: service_healthy }' "$stack"
grep -qF '      - s3-data:/data' "$stack"
grep -qF '  s3-data:' "$stack"

caddyfile=$root/deploy/reverse-proxy/Caddyfile.example
grep -qF 'timeline.example.com' "$caddyfile"
grep -qF '/mcp/timeline' "$caddyfile"
# Caddy refuses to adapt a block whose first token shares the line with its opening brace:
# `handle /api/* { respond 404 }` is a startup error, not a style choice. The two-domain-set
# acceptance adapts this file and caught exactly this once, so check the shape here as well.
if grep -nE '\{[[:space:]]+[^}]*\}[[:space:]]*$' "$caddyfile"; then
	printf '%s\n' "Caddyfile.example: a block opens and closes on one line; write it out on its own lines" >&2
	exit 1
fi

publish=$root/.github/workflows/docker-publish.yml
grep -qF 'VITE_API_BASE_URL=https://api.example.com/api' "$publish"
grep -qF 'VITE_OZID_ORIGIN=https://id.example.com' "$publish"

# Cloudflare Pages reads a Wrangler file from the project's root directory and then owns the output
# directory and `vars`. Every Pages project of this repository keeps a blank root directory (the
# repository root), so a wrangler file there would take over homepage, trip-map and Timeline alike
# (it did, in #120: Timeline built with "pages_build_output_dir: apps/homepage/dist" and no variables).
# Each app is configured on its own: homepage's file lives in its own directory, Timeline has none.
for stray in wrangler.json wrangler.jsonc wrangler.toml; do
	if test -e "$root/$stray"; then
		printf '%s\n' "$stray at the repository root would configure every Pages project; move it to apps/<app>/ (docs/monorepo.md)" >&2
		exit 1
	fi
done
homepage_pages=$root/apps/homepage/wrangler.json
test -f "$homepage_pages" || { echo "missing apps/homepage/wrangler.json" >&2; exit 1; }
grep -qF '"pages_build_output_dir": "./dist"' "$homepage_pages"
grep -qF '"name": "oz-homepage"' "$homepage_pages"
for app in timeline trip-map metro ozid admin; do
	for stray in wrangler.json wrangler.jsonc wrangler.toml; do
		if test -e "$root/apps/$app/$stray"; then
			printf '%s\n' "apps/$app/$stray: this app's Pages project is configured in the dashboard (docs/monorepo.md)" >&2
			exit 1
		fi
	done
done
# Timeline keeps its build gate: the Pages recipe is the root script, and it must still refuse a build
# without both public addresses instead of shipping a page that cannot reach its API.
grep -qF '"build:timeline"' "$root/package.json"
grep -qF 'VITE_API_BASE_URL' "$root/apps/timeline/vite.config.ts"
grep -qF 'VITE_OZID_ORIGIN' "$root/apps/timeline/vite.config.ts"
grep -qF 'npm run build:timeline' "$root/docs/monorepo.md"
grep -qF 'apps/timeline/dist' "$root/docs/monorepo.md"
grep -qF 'apps/homepage/dist' "$root/docs/monorepo.md"

printf '%s\n' 'timeline deployment configuration checks passed'
