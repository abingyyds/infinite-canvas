# 构建 Vite 前端产物。
FROM oven/bun:1.3.13 AS web-build

WORKDIR /app/web
COPY web/package.json web/bun.lock ./
RUN bun install
COPY VERSION /app/VERSION
COPY CHANGELOG.md /app/CHANGELOG.md
COPY web ./
RUN bun run build

# 官方插件和应用从同一份源码构建，托管版按本地清单加载。
FROM node:22-alpine AS plugin-build

WORKDIR /app/plugins/canvas
COPY plugins/canvas ./
RUN npm --prefix registry ci --no-audit --no-fund && npm --prefix registry run build

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

# 运行镜像：nginx 对外提供静态前端，并把 /api/* 转发给容器内的 Go 后端。
FROM nginx:1.27-alpine

ENV PORT=3000
ENV API_PORT=18080

COPY --from=web-build /app/web/dist /usr/share/nginx/html
COPY --from=plugin-build /app/plugins/canvas/registry/dist/ /usr/share/nginx/html/official-plugins/
COPY --from=api-build /server /app/server
# 官方镜像的 20-envsubst-on-templates.sh 会把 ${PORT}/${API_PORT} 渲染进 default.conf。
COPY nginx.conf /etc/nginx/templates/default.conf.template
COPY web/docker-entrypoint.sh /docker-entrypoint.d/40-runtime-config.sh
COPY docker-entrypoint-api.sh /docker-entrypoint.d/50-start-api.sh
RUN chmod +x /docker-entrypoint.d/40-runtime-config.sh /docker-entrypoint.d/50-start-api.sh

EXPOSE 3000
