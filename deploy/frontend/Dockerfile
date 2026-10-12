# syntax=docker/dockerfile:1
FROM node:24-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY index.html vite.config.ts tsconfig.json tsconfig.app.json tsconfig.node.json ./
COPY src src
COPY public public
COPY scripts scripts
# Optional OSM raster tile source, baked in at build time (see docs/data-and-maps.md).
ARG VITE_OSM_TILE_URL
ARG VITE_OSM_TILE_MAX_ZOOM
ARG VITE_OSM_TILE_ATTRIBUTION
RUN npm test && npm run check:maps && npm run build

FROM nginx:stable-alpine
ENV BACKEND_UPSTREAM=backend:18081 NGINX_ENVSUBST_FILTER=BACKEND_UPSTREAM
COPY deploy/frontend/default.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
