# 构建 Next.js 前端产物。
FROM oven/bun:1.3.13 AS web-build

WORKDIR /app/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY VERSION /app/VERSION
COPY CHANGELOG.md /app/CHANGELOG.md
COPY web ./
RUN bun run build

# 构建 Go 后端入口。
FROM golang:1.25-alpine AS api-build

WORKDIR /app
COPY go.mod go.sum ./
COPY config ./config
COPY handler ./handler
COPY middleware ./middleware
COPY model ./model
COPY repository ./repository
COPY router ./router
COPY service ./service
COPY main.go ./
RUN go build -o /server .

# 运行镜像：Next.js 对外监听 Railway 注入的 PORT，Go 只在容器内部监听 API_PORT。
FROM node:22-bookworm-slim

WORKDIR /app
COPY VERSION /app/VERSION
COPY CHANGELOG.md /app/CHANGELOG.md
COPY --from=api-build /server /app/server
COPY --from=web-build /app/web/public /app/web/public
COPY --from=web-build /app/web/.next/standalone /app/web
COPY --from=web-build /app/web/.next/static /app/web/.next/static
ENV NODE_ENV=production
ENV HOSTNAME=0.0.0.0
ENV PROMPT_DATA_DIR=/app/data/prompts
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
RUN mkdir -p /app/data/prompts

EXPOSE 3000
# 先启动内部 Go API，再由 Next.js 提供页面并代理 /api/*。
CMD ["sh", "-c", "API_PORT=${API_PORT:-18080}; PORT=$API_PORT /app/server & cd /app/web && API_BASE_URL=http://127.0.0.1:$API_PORT PORT=${PORT:-3000} node server.js"]
