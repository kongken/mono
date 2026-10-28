FROM node:24-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
COPY apps/homepage/package.json apps/homepage/package.json
RUN npm ci
COPY apps/homepage apps/homepage
RUN npm run build -w @kongken/homepage

FROM nginx:1.27-alpine
COPY deploy/homepage/default.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /app/apps/homepage/dist /usr/share/nginx/html
EXPOSE 8080
