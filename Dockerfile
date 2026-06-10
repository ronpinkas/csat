# Build a static csat binary (pure-Go SQLite, so CGO off) and ship it on a tiny
# Alpine image with CA certs + timezone data. Runs as a non-root user; /data is
# a volume (named volumes inherit its ownership on first creation).
FROM golang:1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/csat ./cmd/csat

FROM alpine:3
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -u 10001 csat \
 && mkdir -p /data /etc/csat && chown csat:csat /data
COPY --from=build /out/csat /usr/local/bin/csat
USER csat
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/csat"]
CMD ["-config", "/etc/csat/config.toml"]
