#!/bin/sh
# Stops a panel's own processes so they stay down through a reboot, and leaves its reverse
# proxy and the deployed app alone. Run as root. Usage: stop-panel.sh windlass|coolify|dokploy
set -eu
case "${1:?panel}" in
  windlass)
    # Caddy is the proxy and keeps running.
    systemctl disable --now windlass windlass-docker-proxy ;;
  coolify)
    # coolify-proxy (Traefik) and its error-page container keep running.
    for c in coolify coolify-db coolify-redis coolify-realtime coolify-sentinel; do
      docker inspect "$c" >/dev/null 2>&1 || continue
      docker update --restart=no "$c" >/dev/null
      docker stop "$c" >/dev/null
    done ;;
  dokploy)
    # dokploy-traefik keeps running.
    for s in dokploy dokploy-postgres dokploy-redis; do
      docker service inspect "$s" >/dev/null 2>&1 && docker service scale "$s=0" >/dev/null 2>&1
    done ;;
esac
docker ps --format '{{.Names}}' | sort | tr '\n' ' '
echo
