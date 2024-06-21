FROM golang:1.22-alpine AS builder
RUN apk update && apk upgrade && apk add --no-cache make
WORKDIR /app
COPY . .
RUN make build

FROM alpine:latest
WORKDIR /
COPY --from=builder /app/monica .
RUN cp monica /bin/monica
CMD ["./monica"]

