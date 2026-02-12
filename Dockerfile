# syntax=docker/dockerfile:1

FROM golang:1.25-bookworm AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/birdy .

FROM node:22-bookworm-slim

ARG CLAUDE_CODE_VERSION=2.1.39
ARG BIRD_VERSION=0.8.0
RUN npm install -g "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" "@steipete/bird@${BIRD_VERSION}" \
    && npm cache clean --force

WORKDIR /app

COPY --from=builder /out/birdy /usr/local/bin/birdy
COPY scripts/entrypoint-railway.sh /usr/local/bin/entrypoint-railway

RUN /usr/local/bin/birdy version >/dev/null \
    && chmod +x /usr/local/bin/entrypoint-railway \
    /usr/local/lib/node_modules/@steipete/bird/dist/cli.js \
    && mkdir -p /data/.config/birdy

ENV HOME=/data
ENV XDG_CONFIG_HOME=/data/.config
ENV BIRDY_BIRD_PATH=/usr/local/lib/node_modules/@steipete/bird/dist/cli.js
ENV NODE_ENV=production

EXPOSE 8787

ENTRYPOINT ["/usr/local/bin/entrypoint-railway"]
