FROM golang:1.26 AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd cmd
COPY internal internal
COPY pkg pkg
# The region index test checks the backend copy against the frontend one.
COPY src/regions.json src/regions.json
RUN go test ./...
RUN CGO_ENABLED=0 go build -trimpath -o /tripmap ./cmd/tripmap

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /tripmap /tripmap
ENV PORT=18081 BUTTERFLY_CONFIG_TYPE=file BUTTERFLY_CONFIG_FILE_PATH=/config/config.yaml
EXPOSE 18081
ENTRYPOINT ["/tripmap"]
