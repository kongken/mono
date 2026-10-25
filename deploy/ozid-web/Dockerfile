# syntax=docker/dockerfile:1
FROM node:24-alpine AS build
WORKDIR /app
# Workspace manifests first so the dependency layer is cached until one changes.
COPY package.json package-lock.json ./
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
# The browser tests (apps/ozid/tests) are not needed to build the image.
COPY apps/ozid/index.html apps/ozid/vite.config.ts apps/ozid/tsconfig.json apps/ozid/tsconfig.app.json apps/ozid/tsconfig.node.json apps/ozid/
COPY apps/ozid/src apps/ozid/src
COPY apps/ozid/public apps/ozid/public
# The BFF's canonical public API base, an absolute https URL such as https://api.example.com/api.
# OzID has no same-origin proxy (the session Cookie belongs to the API host), so there is no
# default: the build fails without it, and so does a relative or non-https value.
ARG VITE_API_BASE_URL
ENV VITE_API_BASE_URL=$VITE_API_BASE_URL
RUN npm run test -w @kongken/ozid && npm run build -w @kongken/ozid

FROM nginx:stable-alpine
# API_ORIGIN is the origin of VITE_API_BASE_URL (scheme, host, port): the only address the page's
# Content-Security-Policy lets scripts connect to besides this site.
ENV API_ORIGIN= NGINX_ENVSUBST_FILTER=API_ORIGIN
COPY deploy/ozid-web/default.conf.template /etc/nginx/templates/default.conf.template
COPY deploy/ozid-web/40-require-api-origin.sh /docker-entrypoint.d/40-require-api-origin.sh
RUN chmod +x /docker-entrypoint.d/40-require-api-origin.sh
COPY --from=build /app/apps/ozid/dist /usr/share/nginx/html
EXPOSE 80
