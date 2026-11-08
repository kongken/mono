#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
required="config.timeline.example.yaml deploy/timeline/Dockerfile deploy/timeline-web/Dockerfile deploy/timeline-web/default.conf.template deploy/compose.stack.example.yaml deploy/reverse-proxy/Caddyfile.example"
for file in $required; do test -f "$root/$file" || { echo "missing $file" >&2; exit 1; }; done
rg -q '^db_store: timeline$' "$root/config.timeline.example.yaml"
rg -q '^    timeline:$' "$root/config.timeline.example.yaml"
rg -q 'public_key_file: dev-secrets/bff.pub' "$root/config.timeline.example.yaml"
rg -q 'image: ghcr.io/kongken/mono-timeline:main' "$root/deploy/compose.stack.example.yaml"
rg -q 'image: ghcr.io/kongken/mono-timeline-web:main' "$root/deploy/compose.stack.example.yaml"
rg -q 'timeline.example.com' "$root/deploy/reverse-proxy/Caddyfile.example"
rg -q '/mcp/timeline' "$root/deploy/reverse-proxy/Caddyfile.example"
rg -q "VITE_API_BASE_URL=https://api.example.com/api" "$root/.github/workflows/docker-publish.yml"
rg -q "VITE_OZID_ORIGIN=https://id.example.com" "$root/.github/workflows/docker-publish.yml"
printf '%s\n' 'timeline deployment configuration checks passed'
