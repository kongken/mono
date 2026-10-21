# syntax=docker/dockerfile:1
FROM node:24-alpine AS build
WORKDIR /app
# Workspace manifests first so the dependency layer is cached until one changes.
COPY package.json package-lock.json ./
COPY apps/trip-map/package.json apps/trip-map/package.json
COPY packages/ui/package.json packages/ui/package.json
COPY packages/api-client/package.json packages/api-client/package.json
COPY packages/geo-data/package.json packages/geo-data/package.json
COPY packages/fixtures/package.json packages/fixtures/package.json
RUN npm ci
COPY packages packages
# The browser tests (apps/trip-map/tests) are not needed to build the image.
COPY apps/trip-map/index.html apps/trip-map/vite.config.ts apps/trip-map/tsconfig.json apps/trip-map/tsconfig.app.json apps/trip-map/tsconfig.node.json apps/trip-map/
COPY apps/trip-map/src apps/trip-map/src
COPY apps/trip-map/public apps/trip-map/public
COPY apps/trip-map/scripts apps/trip-map/scripts
# The catalog regression checks the build recipe's required input list too.
COPY deploy/trip-map-web/Dockerfile deploy/trip-map-web/Dockerfile
# check:maps and the share tests compare the server-side preview geometry with the app.
COPY internal/tripmap/share/assets/geometry.json internal/tripmap/share/assets/geometry.json
# F10 checks bind the frontend catalog to the exact backend index and manifest.
COPY internal/tripmap/regions/regions.json internal/tripmap/regions/regions.json
COPY internal/tripmap/regions/catalog.json internal/tripmap/regions/catalog.json
# Optional backend address; empty keeps the static site standalone (local-only).
ARG VITE_API_BASE_URL
ARG VITE_OAUTH_APP_ID=trip-map
# Optional OSM raster tile source, baked in at build time (see docs/trip-map/data-and-maps.md).
ARG VITE_OSM_TILE_URL
ARG VITE_OSM_TILE_MAX_ZOOM
ARG VITE_OSM_TILE_ATTRIBUTION
ARG VITE_OSM_TILE_PROVIDER
ARG VITE_OSM_TILE_VERSION
RUN npm run test:trip-map && npm run check:maps && npm run build:trip-map

FROM nginx:stable-alpine
ENV BACKEND_UPSTREAM=backend:18081 NGINX_ENVSUBST_FILTER=BACKEND_UPSTREAM
COPY deploy/trip-map-web/default.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /app/apps/trip-map/dist /usr/share/nginx/html
EXPOSE 80
