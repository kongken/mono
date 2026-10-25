# syntax=docker/dockerfile:1
FROM node:24-alpine AS build
WORKDIR /app
# Workspace manifests first so the dependency layer is cached until one changes.
COPY package.json package-lock.json ./
COPY apps/admin/package.json apps/admin/package.json
COPY apps/ozid/package.json apps/ozid/package.json
COPY apps/trip-map/package.json apps/trip-map/package.json
COPY apps/metro/package.json apps/metro/package.json
COPY packages/ui/package.json packages/ui/package.json
COPY packages/account/package.json packages/account/package.json
COPY packages/api-client/package.json packages/api-client/package.json
COPY packages/geo-data/package.json packages/geo-data/package.json
COPY packages/fixtures/package.json packages/fixtures/package.json
RUN npm ci
COPY packages packages
# The browser tests (apps/admin/tests) are not needed to build the image.
COPY apps/admin/index.html apps/admin/vite.config.ts apps/admin/tsconfig.json apps/admin/tsconfig.app.json apps/admin/tsconfig.node.json apps/admin/
COPY apps/admin/src apps/admin/src
COPY apps/admin/public apps/admin/public
# Two public build settings, no defaults (the build fails without them, and so does a relative,
# non-https or malformed value):
#   VITE_API_BASE_URL  the BFF's canonical public API base, e.g. https://api.example.com/api.
#                      The console has no same-origin proxy: the session Cookie belongs to the API host.
#   VITE_OZID_ORIGIN   OzID's origin, e.g. https://id.example.com: where sign-in and password
#                      confirmation happen. The console has no login form of its own.
ARG VITE_API_BASE_URL
ARG VITE_OZID_ORIGIN
ENV VITE_API_BASE_URL=$VITE_API_BASE_URL VITE_OZID_ORIGIN=$VITE_OZID_ORIGIN
RUN npm run test -w @kongken/admin && npm run build -w @kongken/admin

FROM nginx:stable-alpine
# Runtime settings, all required (see 19-admin-access.sh: the container refuses to start without
# a usable value, and an empty allowlist is a refusal, not "open"):
#   API_ORIGIN             origin of VITE_API_BASE_URL: the only address the page's CSP lets scripts call
#   ADMIN_HOST             the one host name the console answers to
#   ADMIN_ALLOWED_CIDRS    source networks that may load the console (comma-separated)
# Optional:
#   ADMIN_TRUSTED_PROXIES  gateways in front of this container whose forwarded header is believed;
#                          must not overlap ADMIN_ALLOWED_CIDRS (the container refuses to start)
#   ADMIN_REAL_IP_HEADER   that header (default X-Forwarded-For)
ENV API_ORIGIN= ADMIN_HOST= ADMIN_ALLOWED_CIDRS= ADMIN_TRUSTED_PROXIES= ADMIN_REAL_IP_HEADER=X-Forwarded-For NGINX_ENVSUBST_FILTER=API_ORIGIN|ADMIN_HOST
COPY deploy/admin-web/templates /etc/nginx/templates
COPY deploy/admin-web/19-admin-access.sh /docker-entrypoint.d/19-admin-access.sh
RUN chmod +x /docker-entrypoint.d/19-admin-access.sh
COPY --from=build /app/apps/admin/dist /usr/share/nginx/html
EXPOSE 80
