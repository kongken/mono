# syntax=docker/dockerfile:1
FROM node:24-alpine AS build
WORKDIR /app
# Workspace manifests first so the dependency layer is cached until one changes.
COPY package.json package-lock.json ./
COPY apps/web/package.json apps/web/package.json
COPY packages/ui/package.json packages/ui/package.json
COPY packages/api-client/package.json packages/api-client/package.json
COPY packages/geo-data/package.json packages/geo-data/package.json
COPY packages/fixtures/package.json packages/fixtures/package.json
RUN npm ci
COPY packages packages
# The browser tests (apps/web/tests) are not needed to build the image.
COPY apps/web/index.html apps/web/vite.config.ts apps/web/tsconfig.json apps/web/tsconfig.app.json apps/web/tsconfig.node.json apps/web/
COPY apps/web/src apps/web/src
COPY apps/web/public apps/web/public
COPY apps/web/scripts apps/web/scripts
# The catalog regression checks the build recipe's required input list too.
COPY deploy/frontend/Dockerfile deploy/frontend/Dockerfile
# check:maps and the share tests compare the server-side preview geometry with the app.
COPY internal/tripmap/share/assets/geometry.json internal/tripmap/share/assets/geometry.json
# F10 checks bind the frontend catalog to the exact backend index and manifest.
COPY internal/tripmap/regions/regions.json internal/tripmap/regions/regions.json
COPY internal/tripmap/regions/catalog.json internal/tripmap/regions/catalog.json
# Optional backend address; empty keeps the static site standalone (local-only).
ARG VITE_API_BASE_URL
# Optional OSM raster tile source, baked in at build time (see docs/data-and-maps.md).
ARG VITE_OSM_TILE_URL
ARG VITE_OSM_TILE_MAX_ZOOM
ARG VITE_OSM_TILE_ATTRIBUTION
RUN npm test && npm run check:maps && npm run build

FROM nginx:stable-alpine
ENV BACKEND_UPSTREAM=backend:18081 NGINX_ENVSUBST_FILTER=BACKEND_UPSTREAM
COPY deploy/frontend/default.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /app/apps/web/dist /usr/share/nginx/html
EXPOSE 80
