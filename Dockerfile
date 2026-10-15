FROM golang:1.26 AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
# tripmap and the shared platform code only: the build fails if tripmap ever
# imports another service.
COPY cmd/tripmap cmd/tripmap
COPY internal/tripmap internal/tripmap
COPY internal/platform internal/platform
COPY pkg pkg
# The region index and city coordinate tests check the backend copies against the frontend ones.
COPY src/regions.json src/regions.json
COPY src/city-coords.json src/city-coords.json
COPY scripts/fixtures scripts/fixtures
COPY migrations/tripmap migrations/tripmap
RUN go test ./internal/... ./cmd/...
RUN CGO_ENABLED=0 go build -trimpath -o /tripmap ./cmd/tripmap

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /tripmap /tripmap
ENV PORT=18081 BUTTERFLY_CONFIG_TYPE=file BUTTERFLY_CONFIG_FILE_PATH=/config/config.yaml
EXPOSE 18081
ENTRYPOINT ["/tripmap"]
