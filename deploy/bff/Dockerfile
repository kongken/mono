FROM golang:1.26 AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
# Only the BFF and the shared platform code: the build fails if the BFF ever
# imports a business package.
COPY cmd/bff cmd/bff
COPY internal/bff internal/bff
COPY internal/platform internal/platform
# Generated gRPC and Connect contracts shared with the services.
COPY pkg pkg
# The example configuration is checked against the route table.
COPY config.bff.example.yaml config.bff.example.yaml
RUN go test ./internal/bff/... ./internal/platform/...
RUN CGO_ENABLED=0 go build -trimpath -o /bff ./cmd/bff

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /bff /bff
ENV PORT=18081 BUTTERFLY_CONFIG_TYPE=file BUTTERFLY_CONFIG_FILE_PATH=/config/config.yaml
EXPOSE 18081
ENTRYPOINT ["/bff"]
