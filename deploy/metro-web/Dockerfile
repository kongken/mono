# syntax=docker/dockerfile:1
FROM node:24-alpine AS build
WORKDIR /app
# Workspace manifests first so the dependency layer is cached until one changes.
COPY package.json package-lock.json ./
COPY apps/metro/package.json apps/metro/package.json
COPY apps/trip-map/package.json apps/trip-map/package.json
COPY apps/admin/package.json apps/admin/package.json
COPY apps/ozid/package.json apps/ozid/package.json
COPY packages/ui/package.json packages/ui/package.json
COPY packages/api-client/package.json packages/api-client/package.json
COPY packages/geo-data/package.json packages/geo-data/package.json
COPY packages/fixtures/package.json packages/fixtures/package.json
COPY packages/metro-core/package.json packages/metro-core/package.json
RUN npm ci
COPY packages packages
# The browser tests (apps/metro/tests) are not needed to build the image.
COPY apps/metro/index.html apps/metro/vite.config.ts apps/metro/tsconfig.json apps/metro/tsconfig.app.json apps/metro/tsconfig.node.json apps/metro/
COPY apps/metro/src apps/metro/src
COPY apps/metro/public apps/metro/public
# The browser talks to the BFF through the same origin, so the API base is /api.
ARG VITE_API_BASE_URL=/api
ENV VITE_API_BASE_URL=$VITE_API_BASE_URL
RUN npm run test:metro && npm run build:metro

FROM nginx:stable-alpine
ENV BACKEND_UPSTREAM=bff:18081 NGINX_ENVSUBST_FILTER=BACKEND_UPSTREAM
COPY deploy/metro-web/default.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /app/apps/metro/dist /usr/share/nginx/html
EXPOSE 80
