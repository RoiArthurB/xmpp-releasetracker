# ── Build stage ───────────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0: modernc/sqlite is pure Go, no C toolchain needed.
# -s -w: strip debug info for a smaller binary.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o xmpp-releasetracker .

# ── Runtime stage ─────────────────────────────────────────────────────────────
FROM alpine:3.21

# ca-certificates: required for HTTPS calls to GitHub/GitLab/Gitea APIs.
# tzdata: lets Go parse timezone names used in release timestamps.
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=builder /build/xmpp-releasetracker .

# /data is where the SQLite database lives (mount a volume here).
VOLUME ["/data"]

ENTRYPOINT ["./xmpp-releasetracker"]
CMD ["-config", "/etc/xmpp-releasetracker/config.yml"]
