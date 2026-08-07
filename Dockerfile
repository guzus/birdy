# syntax=docker/dockerfile:1

FROM oven/bun:1.2.23 AS webbuilder
WORKDIR /web

COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile

COPY web/ ./
RUN bun run build

FROM golang:1.26.5-bookworm AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/birdy .

FROM node:22-bookworm-slim

# Node stays because Claude Code is a Node application. bird does not: birdy
# serves every command natively, so the image no longer installs it.
ARG CLAUDE_CODE_VERSION=2.1.207
RUN npm install -g "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
    && npm cache clean --force

WORKDIR /app

COPY e2b-runner/package.json e2b-runner/package-lock.json /app/e2b-runner/
RUN cd /app/e2b-runner \
    && npm ci --omit=dev --ignore-scripts \
    && npm cache clean --force

COPY --from=builder /out/birdy /usr/local/bin/birdy
COPY --from=webbuilder /web/dist /app/web/dist
COPY e2b-runner/config.mjs e2b-runner/lifecycle.mjs e2b-runner/claude.mjs /app/e2b-runner/
COPY scripts/entrypoint-railway.sh /usr/local/bin/entrypoint-railway

RUN /usr/local/bin/birdy version >/dev/null \
    && chmod +x /usr/local/bin/entrypoint-railway \
    && mkdir -p /data/.config/birdy

ENV HOME=/data
ENV XDG_CONFIG_HOME=/data/.config
ENV BIRDY_E2B_RUNNER_PATH=/app/e2b-runner/claude.mjs
ENV NODE_ENV=production

EXPOSE 8787

ENTRYPOINT ["/usr/local/bin/entrypoint-railway"]
