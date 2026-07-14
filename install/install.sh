#!/bin/sh
# Windlass installer — https://github.com/windlass-dev/windlass
#
#   curl -fsSL https://get.windlass.sh | sh
#
# Flags:
#   --yes            non-interactive (assume yes)
#   --version vX.Y.Z install a specific version (default: latest)
#   --no-caddy       skip Caddy installation (no automatic HTTPS)
#   --no-docker      skip Docker installation
#   --binary PATH    install a local binary instead of downloading (CI/testing)
set -eu

REPO="windlass-dev/windlass"
DATA_DIR="/var/lib/windlass"
BIN="/usr/local/bin/windlass"
ASSUME_YES=0
VERSION="latest"
WANT_CADDY=1
WANT_DOCKER=1
LOCAL_BINARY=""

log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m ✓ \033[0m%s\n' "$*"; }
fail() { printf '\033[1;31m ✗ %s\033[0m\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --yes) ASSUME_YES=1 ;;
    --version) VERSION="$2"; shift ;;
    --no-caddy) WANT_CADDY=0 ;;
    --no-docker) WANT_DOCKER=0 ;;
    --binary) LOCAL_BINARY="$2"; shift ;;
    *) fail "unknown flag: $1" ;;
  esac
  shift
done

[ "$(id -u)" = "0" ] || fail "run as root (sudo sh install.sh)"
[ "$(uname -s)" = "Linux" ] || fail "Windlass servers run on Linux"

ARCH=$(uname -m)
case "$ARCH" in
  x86_64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) fail "unsupported architecture: $ARCH" ;;
esac

confirm() {
  [ "$ASSUME_YES" = "1" ] && return 0
  printf '%s [y/N] ' "$1"
  read -r answer
  [ "$answer" = "y" ] || [ "$answer" = "Y" ]
}

# --- Docker -----------------------------------------------------------------
if command -v docker >/dev/null 2>&1; then
  ok "docker $(docker --version | sed 's/^Docker version //;s/,.*//') present"
elif [ "$WANT_DOCKER" = "1" ]; then
  confirm "Docker is not installed. Install it via get.docker.com?" || fail "Docker is required"
  log "installing Docker"
  curl -fsSL https://get.docker.com | sh
  ok "docker installed"
else
  fail "Docker is required (rerun without --no-docker)"
fi

docker compose version >/dev/null 2>&1 || fail "docker compose v2 plugin missing (update Docker)"
ok "docker compose $(docker compose version --short)"

command -v git >/dev/null 2>&1 || {
  log "installing git"
  if command -v apt-get >/dev/null 2>&1; then apt-get update -q && apt-get install -y -q git
  elif command -v dnf >/dev/null 2>&1; then dnf install -y -q git
  elif command -v apk >/dev/null 2>&1; then apk add --no-cache git
  else fail "install git manually, then rerun"
  fi
}
ok "git present"

# --- Caddy ------------------------------------------------------------------
if [ "$WANT_CADDY" = "1" ]; then
  if command -v caddy >/dev/null 2>&1; then
    ok "caddy present"
  else
    log "installing Caddy"
    if command -v apt-get >/dev/null 2>&1; then
      apt-get update -q
      apt-get install -y -q debian-keyring debian-archive-keyring apt-transport-https curl gnupg
      curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
        | gpg --batch --yes --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
      curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
        > /etc/apt/sources.list.d/caddy-stable.list
      apt-get update -q && apt-get install -y -q caddy
    elif command -v dnf >/dev/null 2>&1; then
      dnf install -y -q 'dnf-command(copr)' && dnf copr enable -y @caddy/caddy && dnf install -y -q caddy
      systemctl enable --now caddy
    elif command -v apk >/dev/null 2>&1; then
      apk add --no-cache caddy
      rc-update add caddy 2>/dev/null || true
    else
      fail "unsupported package manager; install Caddy manually or rerun with --no-caddy"
    fi
    ok "caddy installed"
  fi
else
  log "skipping Caddy (domains and automatic HTTPS unavailable)"
fi

# --- Windlass binary ----------------------------------------------------------
if [ -n "$LOCAL_BINARY" ]; then
  install -m 0755 "$LOCAL_BINARY" "$BIN"
  ok "installed local binary"
else
  if [ "$VERSION" = "latest" ]; then
    VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
      | grep -o '"tag_name": *"[^"]*"' | head -n1 | sed 's/.*"tag_name": *"//;s/"//')
    [ -n "$VERSION" ] || fail "could not resolve latest release"
  fi
  log "downloading windlass $VERSION ($ARCH)"
  TMP=$(mktemp -d)
  trap 'rm -rf "$TMP"' EXIT
  curl -fsSL -o "$TMP/windlass" \
    "https://github.com/$REPO/releases/download/$VERSION/windlass-linux-$ARCH"
  curl -fsSL -o "$TMP/checksums.txt" \
    "https://github.com/$REPO/releases/download/$VERSION/checksums.txt"
  (cd "$TMP" && grep "windlass-linux-$ARCH\$" checksums.txt | sed "s/windlass-linux-$ARCH/windlass/" | sha256sum -c -) \
    || fail "checksum verification failed"
  install -m 0755 "$TMP/windlass" "$BIN"
  ok "windlass $VERSION installed"
fi

# --- User, directories, service ----------------------------------------------
if ! id windlass >/dev/null 2>&1; then
  useradd --system --home-dir "$DATA_DIR" --shell /usr/sbin/nologin windlass 2>/dev/null \
    || adduser -S -h "$DATA_DIR" -s /sbin/nologin windlass
fi
# Direct Docker socket access is root-equivalent. Windlass uses the restricted
# loopback proxy installed below instead of membership in the docker group.
gpasswd -d windlass docker >/dev/null 2>&1 || true

mkdir -p "$DATA_DIR/projects" "$DATA_DIR/data" "$DATA_DIR/backups"
chown -R windlass:windlass "$DATA_DIR"
chmod 750 "$DATA_DIR"
chmod 700 "$DATA_DIR/data"
chmod 750 "$DATA_DIR/projects" "$DATA_DIR/backups"

if [ -d /etc/systemd/system ]; then
  cat > /etc/systemd/system/windlass-docker-proxy.service <<'PROXYUNIT'
[Unit]
Description=Restricted Docker API proxy for Windlass
After=docker.service
Requires=docker.service

[Service]
Type=simple
ExecStartPre=-/usr/bin/docker rm -f windlass-docker-proxy
ExecStart=/usr/bin/docker run --rm --name windlass-docker-proxy --cap-drop=ALL --security-opt=no-new-privileges -p 127.0.0.1:2375:2375 -v /var/run/docker.sock:/var/run/docker.sock:ro -e POST=1 -e BUILD=1 -e CONTAINERS=1 -e ALLOW_START=1 -e ALLOW_STOP=1 -e ALLOW_RESTARTS=1 -e EVENTS=1 -e EXEC=1 -e IMAGES=1 -e INFO=1 -e NETWORKS=1 -e SYSTEM=1 -e VERSION=1 -e VOLUMES=1 ghcr.io/tecnativa/docker-socket-proxy:v0.4.2
ExecStop=-/usr/bin/docker stop -t 10 windlass-docker-proxy
Restart=always
RestartSec=2
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
PROXYUNIT

  cat > /etc/systemd/system/windlass.service <<'UNIT'
[Unit]
Description=Windlass — Docker Compose control plane
After=network-online.target docker.service windlass-docker-proxy.service
Wants=network-online.target
Requires=docker.service windlass-docker-proxy.service

[Service]
Type=notify
ExecStart=/usr/local/bin/windlass
User=windlass
Group=windlass
Environment=WINDLASS_DATA=/var/lib/windlass
Environment=DOCKER_HOST=tcp://127.0.0.1:2375
Restart=always
RestartSec=2
UMask=0077
NoNewPrivileges=yes
CapabilityBoundingSet=
LockPersonality=yes
MemoryDenyWriteExecute=yes
PrivateDevices=yes
PrivateTmp=yes
ProtectClock=yes
ProtectControlGroups=yes
ProtectKernelModules=yes
ProtectKernelTunables=yes
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
RestrictRealtime=yes
RestrictSUIDSGID=yes
SystemCallArchitectures=native
ReadWritePaths=/var/lib/windlass
ProtectSystem=full
ProtectHome=yes

[Install]
WantedBy=multi-user.target
UNIT
  systemctl daemon-reload
  systemctl enable --now windlass-docker-proxy
  systemctl enable --now windlass
  ok "systemd service started"
else
  log "systemd not found — start manually: WINDLASS_DATA=$DATA_DIR windlass"
fi

IP=$(hostname -I 2>/dev/null | awk '{print $1}' || echo "<server-ip>")
echo
ok "Windlass is running"
echo
echo "  Open:        http://$IP:8080"
echo "  Setup token: journalctl -u windlass | grep setup_token"
echo
echo "  The one-time setup token in the server log claims this instance."
