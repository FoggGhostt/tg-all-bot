FROM golang:1.23-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/bot .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S bot && adduser -S -G bot bot && \
    mkdir -p /data && chown bot:bot /data
USER bot
WORKDIR /data
COPY --from=build /out/bot /usr/local/bin/bot
ENV DB_PATH=/data/bot.db
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/bot"]
