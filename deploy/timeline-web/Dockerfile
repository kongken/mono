# syntax=docker/dockerfile:1
FROM node:24-alpine AS build
WORKDIR /app
# Workspace manifests first so the dependency layer is cached until one changes.
COPY package.json package-lock.json ./
COPY apps/timeline/package.json apps/timeline/package.json
COPY apps/trip-map/package.json apps/trip-map/package.json
COPY apps/admin/package.json apps/admin/package.json
COPY apps/ozid/package.json apps/ozid/package.json
COPY packages/account/package.json packages/account/package.json
COPY packages/ui/package.json packages/ui/package.json
COPY packages/api-client/package.json packages/api-client/package.json
COPY packages/geo-data/package.json packages/geo-data/package.json
COPY packages/fixtures/package.json packages/fixtures/package.json
COPY packages/metro-core/package.json packages/metro-core/package.json
RUN npm ci
COPY packages packages
# The browser tests (apps/timeline/tests) are not needed to build the image.
COPY apps/timeline/index.html apps/timeline/vite.config.ts apps/timeline/tsconfig.json apps/timeline/tsconfig.app.json apps/timeline/tsconfig.node.json apps/timeline/
COPY apps/timeline/src apps/timeline/src
COPY apps/timeline/public apps/timeline/public
COPY apps/timeline/scripts apps/timeline/scripts
# Both addresses are public build settings with no default: the BFF's canonical public API base
# (https://api.example.com/api; the session Cookie belongs to that host, so a same-origin /api proxy of this site
# cannot carry the shared sign-in) and the OzID origin (https://id.example.com; every account page is OzID's).
# A missing or malformed value fails the build.
ARG VITE_API_BASE_URL
ARG VITE_OZID_ORIGIN
ENV VITE_API_BASE_URL=$VITE_API_BASE_URL VITE_OZID_ORIGIN=$VITE_OZID_ORIGIN
RUN npm run test:timeline && npm run build:timeline

FROM nginx:stable-alpine
ENV BACKEND_UPSTREAM=bff:18081 NGINX_ENVSUBST_FILTER=BACKEND_UPSTREAM
COPY deploy/timeline-web/default.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /app/apps/timeline/dist /usr/share/nginx/html
EXPOSE 80
