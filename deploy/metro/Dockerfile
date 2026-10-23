FROM golang:1.26 AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
# metro and the shared platform code only: the build fails if metro ever
# imports another service or the BFF.
COPY cmd/metro cmd/metro
COPY internal/metro internal/metro
COPY internal/platform internal/platform
COPY pkg pkg
COPY migrations/metro migrations/metro
RUN go test ./internal/metro/... ./internal/platform/... ./cmd/metro/...
RUN CGO_ENABLED=0 go build -trimpath -o /metro ./cmd/metro

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /metro /metro
ENV PORT=18084 BUTTERFLY_CONFIG_TYPE=file BUTTERFLY_CONFIG_FILE_PATH=/config/config.yaml
EXPOSE 18084 18085
ENTRYPOINT ["/metro"]
