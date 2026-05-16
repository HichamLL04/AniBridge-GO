FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/anibridge-go ./cmd/anibridge-go

FROM alpine:3.20
RUN apk add --no-cache tzdata ca-certificates \
 && adduser -D -H -u 10001 anibridge-go

WORKDIR /app
COPY --from=builder /out/anibridge-go /app/anibridge-go
COPY frontend/build /app/frontend/build
COPY migrations /app/migrations
COPY config.example.yml /app/config.example.yml
COPY docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod +x /app/docker-entrypoint.sh \
 && mkdir -p /config /app/data \
 && chown -R anibridge-go:anibridge-go /config /app/data

USER anibridge-go

VOLUME /config
EXPOSE 8080

ENTRYPOINT ["/app/docker-entrypoint.sh"]
