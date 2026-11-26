# Minimal deepward image: the static binary and CA certificates only.
# Build tools (mvn, sbt, dotnet) are not included; use deepward as a stage
# or copy /deepward into your build image:
#   COPY --from=deepward /deepward /usr/local/bin/deepward
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w \
      -X github.com/deepward-oss/deepward/internal/buildinfo.Version=${VERSION} \
      -X github.com/deepward-oss/deepward/internal/buildinfo.Commit=${COMMIT} \
      -X github.com/deepward-oss/deepward/internal/buildinfo.Date=${DATE}" \
    -o /deepward ./cmd/deepward

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /deepward /deepward
USER 65532:65532
ENTRYPOINT ["/deepward"]
