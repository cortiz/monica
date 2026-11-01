# Build:  docker build -f other/Dockerfile --build-arg VERSION=1.2.3 -t monica .
# Run:    docker run --rm -v "$PWD:/work" ghcr.io/cortiz/monica run -r 3
#         (the current directory is mounted at /work, the working directory)
#
# Multi-arch: the Go build runs on the build machine's own platform and
# cross-compiles for TARGETOS/TARGETARCH, so `docker buildx build
# --platform linux/amd64,linux/arm64` needs no emulation for the compile.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X jmpeax.com/sec/monica/internal/version.Version=${VERSION}" \
      -o /out/monica .

FROM alpine:3.24
RUN apk add --no-cache ca-certificates \
 && adduser -D -u 10001 monica
COPY --from=builder /out/monica /usr/local/bin/monica
USER monica
WORKDIR /work
ENTRYPOINT ["monica"]
CMD ["--help"]
