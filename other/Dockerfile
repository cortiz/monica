# Build:  docker build -f other/Dockerfile --build-arg VERSION=1.2.3 -t monica .
# Run:    docker run --rm -v "$PWD:/work" monica run -r 3
#         (the current directory is mounted at /work, the working directory)
FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X jmpeax.com/sec/monica/internal/version.Version=${VERSION}" \
      -o /out/monica .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates \
 && adduser -D -u 10001 monica
COPY --from=builder /out/monica /usr/local/bin/monica
USER monica
WORKDIR /work
ENTRYPOINT ["monica"]
CMD ["--help"]
