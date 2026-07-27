#!/bin/sh
set -e

# 由 nginx 官方镜像入口在启动前执行（/docker-entrypoint.d/*.sh），随后 nginx 正常拉起。
# ponytail: 后台起进程即可，没上 supervisor —— Go 挂了不会拉起容器，
# 需要自愈时再换 supervisord 或拆成两个容器。
PORT="${API_PORT:-18080}" /app/server &
