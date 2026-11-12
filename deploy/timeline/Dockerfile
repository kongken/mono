FROM golang:1.26 AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
# metro and the shared platform code only: the build fails if metro ever
# imports another service or the BFF.
COPY cmd/timeline cmd/timeline
COPY internal/timeline internal/timeline
COPY internal/tripmap/blobstore internal/tripmap/blobstore
COPY internal/platform internal/platform
COPY pkg pkg
COPY migrations/timeline migrations/timeline
RUN go test ./internal/timeline/... ./internal/platform/... ./cmd/timeline/...
RUN CGO_ENABLED=0 go build -trimpath -o /timeline ./cmd/timeline

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /timeline /timeline
ENV PORT=18088 BUTTERFLY_CONFIG_TYPE=file BUTTERFLY_CONFIG_FILE_PATH=/config/config.yaml
EXPOSE 18088 18089
ENTRYPOINT ["/timeline"]
