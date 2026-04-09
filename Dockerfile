FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /nv ./cmd/nv/

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /nv /usr/local/bin/nv
COPY docker-entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 443
VOLUME /etc/nightveil

ENTRYPOINT ["/entrypoint.sh"]
