#!/bin/sh
# Windlass installer: https://github.com/Sulaiman-Dauda/windlass
#
#   curl -fsSL https://get.windlass.run | sudo sh
#
# Flags:
#   --yes            non-interactive (assume yes)
#   --version vX.Y   install a specific release, e.g. v0.61 (default: latest)
#   --no-caddy       skip Caddy installation (no automatic HTTPS)
#   --no-docker      skip Docker installation
#   --binary PATH    install a local binary instead of downloading (CI/testing)
#   --caddy-resume   only make the Caddy this script installed resume its last
#                    config after a restart, then exit
set -eu

REPO="Sulaiman-Dauda/windlass"
DATA_DIR="/var/lib/windlass"
BIN="/usr/local/bin/windlass"
ASSUME_YES=0
VERSION="latest"
WANT_CADDY=1
WANT_DOCKER=1
LOCAL_BINARY=""
ONLY_CADDY_RESUME=0

log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m ✓ \033[0m%s\n' "$*"; }
warn() { printf '\033[1;33m ! \033[0m%s\n' "$*"; }
fail() { printf '\033[1;31m ✗ %s\033[0m\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --yes) ASSUME_YES=1 ;;
    --version) VERSION="$2"; shift ;;
    --no-caddy) WANT_CADDY=0 ;;
    --no-docker) WANT_DOCKER=0 ;;
    --binary) LOCAL_BINARY="$2"; shift ;;
    --caddy-resume) ONLY_CADDY_RESUME=1 ;;
    *) fail "unknown flag: $1" ;;
  esac
  shift
done

[ "$(id -u)" = "0" ] || fail "run as root (sudo sh install.sh)"
[ "$(uname -s)" = "Linux" ] || fail "Windlass servers run on Linux"

# --- Caddy resume ---------------------------------------------------------------
# Windlass adds its routes through the Caddy admin API. Caddy saves each
# change to its autosave file but by default starts from the Caddyfile, so a
# Caddy restart drops every route until Windlass is running again to put them
# back. This drop-in makes Caddy start from the config Windlass last applied
# (--resume falls back to the Caddyfile when there is none) and makes a reload
# a no-op, since the only thing a reload could load is the admin-only file.
#
# Only done when the Caddyfile is still the admin-only one this script writes:
# on a Caddy someone configured, resuming would ignore their Caddyfile edits.
# After a reboot containers can come back on new IPs, so the resumed routes can
# point at the wrong address until Windlass starts and syncs them.
CADDY_DROPIN=/etc/systemd/system/caddy.service.d/windlass-resume.conf
caddyfile_is_ours() {
  [ -f /etc/caddy/Caddyfile ] || return 1
  [ "$(tr -d ' \t\n' </etc/caddy/Caddyfile)" = "{admin127.0.0.1:2019}" ]
}
caddy_resume() {
  if ! command -v systemctl >/dev/null 2>&1 || [ ! -d /run/systemd/system ]; then
    warn "Caddy resume needs systemd; routes return when Windlass starts"
    return 0
  fi
  if ! caddyfile_is_ours; then
    log "Caddy has its own Caddyfile; leaving its restart behaviour alone"
    return 0
  fi
  # The packaged unit runs /usr/bin/caddy; keep that build when it is there.
  CADDY_BIN=/usr/bin/caddy
  [ -x "$CADDY_BIN" ] || CADDY_BIN=$(command -v caddy) || { warn "caddy not found"; return 0; }
  WANT=$(cat <<DROPIN
# Written by the Windlass installer. Caddy starts from the config Windlass last
# applied, so domains keep serving across a Caddy restart. A reload is a no-op,
# and Caddy ignores later edits to /etc/caddy/Caddyfile while this file exists.
# Undo: rm $CADDY_DROPIN && systemctl daemon-reload && systemctl restart caddy
[Service]
ExecStart=
ExecStart=$CADDY_BIN run --environ --resume --config /etc/caddy/Caddyfile
ExecReload=
ExecReload=/bin/true
DROPIN
)
  if [ -f "$CADDY_DROPIN" ] && [ "$(cat "$CADDY_DROPIN")" = "$WANT" ]; then
    ok "Caddy already resumes its routes after a restart"
    return 0
  fi
  [ -f "$CADDY_DROPIN" ] && cp "$CADDY_DROPIN" "$CADDY_DROPIN.bak"
  mkdir -p "$(dirname "$CADDY_DROPIN")"
  printf '%s\n' "$WANT" > "$CADDY_DROPIN"
  systemctl daemon-reload
  # Only restart a Caddy that was running; a restart resumes the running
  # config, so no route is lost.
  if ! systemctl is-active --quiet caddy; then
    rm -f "$CADDY_DROPIN.bak"
    ok "Caddy will resume its routes after a restart (it is not running now)"
    return 0
  fi
  if systemctl restart caddy; then
    rm -f "$CADDY_DROPIN.bak"
    ok "Caddy resumes its routes after a restart"
    return 0
  fi
  # Put Caddy back as it was rather than leave every domain offline.
  warn "Caddy did not start with --resume; removing the drop-in"
  if [ -f "$CADDY_DROPIN.bak" ]; then mv -f "$CADDY_DROPIN.bak" "$CADDY_DROPIN"; else rm -f "$CADDY_DROPIN"; fi
  systemctl daemon-reload
  systemctl restart caddy || warn "Caddy is not running: check journalctl -u caddy"
  [ "$ONLY_CADDY_RESUME" = "1" ] && fail "Caddy resume not applied"
  return 0
}

if [ "$ONLY_CADDY_RESUME" = "1" ]; then
  caddy_resume
  exit 0
fi

ARCH=$(uname -m)
case "$ARCH" in
  x86_64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) fail "unsupported architecture: $ARCH" ;;
esac

confirm() {
  [ "$ASSUME_YES" = "1" ] && return 0
  # Under curl | sh, stdin is this script, so ask the terminal instead.
  # With no terminal at all, the answer is no; rerun with --yes.
  answer=""
  if (true </dev/tty) 2>/dev/null; then
    printf '%s [y/N] ' "$1" >/dev/tty
    read -r answer </dev/tty || answer=""
  else
    printf '%s [y/N] no terminal to ask, rerun with --yes\n' "$1" >&2
  fi
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

    # Windlass creates and owns its own :80+:443 server through the Caddy admin
    # API. The distro package ships a :80-only welcome site; leaving it in place
    # makes Windlass graft its routes onto that plain-HTTP server, so
    # application domains never get an HTTPS listener. Replace it with an
    # admin-only base so Windlass's server handles requests. Only done on a
    # fresh install (the "caddy present" branch above leaves existing config
    # untouched).
    if [ -f /etc/caddy/Caddyfile ]; then
      cp /etc/caddy/Caddyfile /etc/caddy/Caddyfile.default.bak 2>/dev/null || true
    fi
    mkdir -p /etc/caddy
    cat > /etc/caddy/Caddyfile <<'CADDYFILE'
{
	admin 127.0.0.1:2019
}
CADDYFILE
    systemctl reload caddy 2>/dev/null \
      || systemctl restart caddy 2>/dev/null \
      || rc-service caddy restart 2>/dev/null \
      || true
  fi
  caddy_resume
else
  log "skipping Caddy (domains and automatic HTTPS unavailable)"
fi

# --- Windlass binary ----------------------------------------------------------
# A rerun on a server where Windlass already runs is an upgrade: the service
# restarts on the new binary below. Deployed containers keep running.
UPGRADE=0
if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet windlass 2>/dev/null; then
  UPGRADE=1
  warn "Windlass is already running here, so this rerun upgrades it."
  echo "    The panel restarts on the new binary; deployed apps keep running."
fi

if [ -n "$LOCAL_BINARY" ]; then
  install -o root -g root -m 0755 "$LOCAL_BINARY" "$BIN.new" && mv -f "$BIN.new" "$BIN"
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
  install -o root -g root -m 0755 "$TMP/windlass" "$BIN.new" && mv -f "$BIN.new" "$BIN"
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
ExecStartPre=-/usr/bin/docker pull ghcr.io/tecnativa/docker-socket-proxy:v0.4.2
ExecStart=/usr/bin/docker run --rm --name windlass-docker-proxy --cap-drop=ALL --security-opt=no-new-privileges -p 127.0.0.1:2375:2375 -v /var/run/docker.sock:/var/run/docker.sock:ro -e POST=1 -e BUILD=1 -e CONTAINERS=1 -e ALLOW_START=1 -e ALLOW_STOP=1 -e ALLOW_RESTARTS=1 -e EVENTS=1 -e EXEC=1 -e IMAGES=1 -e INFO=1 -e NETWORKS=1 -e SYSTEM=1 -e VERSION=1 -e VOLUMES=1 ghcr.io/tecnativa/docker-socket-proxy:v0.4.2
ExecStartPost=/bin/sh -c 'timeout 90 sh -c "until /usr/bin/docker -H tcp://127.0.0.1:2375 version >/dev/null 2>&1; do sleep 1; done"'
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
# Self-update: the panel stages a verified binary in its data dir (it cannot
# write /usr/local/bin under ProtectSystem=); this privileged hook swaps it
# in before every start, keeping the old binary as windlass.previous. The
# staged file is read as the windlass user, so a symlink planted there cannot
# make root copy a file windlass may not read; root writes a root-owned copy
# so the service user can never rewrite the binary it runs. A failed swap
# leaves the staged file for the next start and starts the current binary.
ExecStartPre=+/bin/sh -c "if [ -x /var/lib/windlass/data/update/windlass.next ]; then install -o root -g root -m 0755 /usr/local/bin/windlass /usr/local/bin/windlass.previous 2>/dev/null || true; if runuser -u windlass -- cat /var/lib/windlass/data/update/windlass.next > /usr/local/bin/windlass.new && chown root:root /usr/local/bin/windlass.new && chmod 0755 /usr/local/bin/windlass.new && mv -f /usr/local/bin/windlass.new /usr/local/bin/windlass; then rm -f /var/lib/windlass/data/update/windlass.next; else rm -f /usr/local/bin/windlass.new; echo windlass: update swap failed, starting the current binary >&2; fi; fi"
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
  # Installs before v0.3 carried the swap hook in a drop-in that moved the
  # service user's file into place. The unit above has the root-owned hook,
  # so the old drop-in goes: if it reset ExecStartPre= it would drop the new
  # hook, and its mv is never wanted again.
  OLD_HOOK=/etc/systemd/system/windlass.service.d/self-update.conf
  if [ -f "$OLD_HOOK" ] && grep -q 'windlass.next' "$OLD_HOOK"; then
    rm -f "$OLD_HOOK"
    ok "removed the old self-update drop-in"
  fi
  # The binary installed above is the one to run: drop any update the panel
  # staged earlier, and make an older windlass.previous root-owned too.
  rm -f "$DATA_DIR/data/update/windlass.next"
  if [ -f "$BIN.previous" ]; then chown root:root "$BIN.previous" && chmod 0755 "$BIN.previous"; fi
  systemctl daemon-reload
  systemctl enable --now windlass-docker-proxy
  systemctl enable --now windlass
  if [ "$UPGRADE" = "1" ]; then
    systemctl restart windlass
    ok "systemd service restarted on the new binary"
  else
    ok "systemd service started"
  fi
else
  log "systemd not found — start manually: WINDLASS_DATA=$DATA_DIR windlass"
fi

IP=$(hostname -I 2>/dev/null | awk '{print $1}')
[ -n "$IP" ] || IP="<server-ip>"

# --- Firewall ----------------------------------------------------------------
# Read only: the installer never changes a firewall, it says what it sees (#117).
# FIREWALL is ufw, firewalld or empty; port_closed answers for that one.
FIREWALL=""
UFW_STATUS=""
if command -v ufw >/dev/null 2>&1; then
  UFW_STATUS=$(LC_ALL=C ufw status 2>/dev/null || true)
  case "$UFW_STATUS" in "Status: active"*) FIREWALL=ufw ;; esac
fi
if [ -z "$FIREWALL" ] && command -v firewall-cmd >/dev/null 2>&1 \
  && [ "$(firewall-cmd --state 2>/dev/null)" = "running" ]; then
  FIREWALL=firewalld
fi

# port_closed PORT [SERVICE]: true when the firewall found does not allow PORT/tcp.
port_closed() {
  case "$FIREWALL" in
    ufw)
      # Matches "80/tcp ALLOW", "80 ALLOW" and lists such as "80,443/tcp ALLOW".
      ! printf '%s\n' "$UFW_STATUS" | grep -Eq "^([0-9]+,)*$1(,[0-9]+)*(/tcp)?[[:space:]]+ALLOW"
      ;;
    firewalld)
      firewall-cmd --query-port="$1/tcp" >/dev/null 2>&1 && return 1
      [ -n "$2" ] && firewall-cmd --query-service="$2" >/dev/null 2>&1 && return 1
      return 0
      ;;
    *) return 1 ;;
  esac
}

echo
ok "Windlass is running"
echo
if port_closed 8080; then
  echo "  Port 8080 looks closed by $FIREWALL, so open the panel through an SSH tunnel."
  echo "  From your own computer run:"
  echo "    ssh -L 8080:127.0.0.1:8080 ${SUDO_USER:-root}@$IP"
  echo "  then open http://localhost:8080"
else
  echo "  Open:        http://$IP:8080"
fi
# The server logs a setup token only while no user exists, so only point at
# it when the panel says it still needs setup. If the panel does not answer
# in time, print the hint: it is harmless on a claimed instance.
NEEDS_SETUP=1
tries=0
while [ "$tries" -lt 30 ]; do
  STATUS=$(curl -fsS --max-time 2 http://127.0.0.1:8080/api/v1/auth/status 2>/dev/null) && {
    case "$STATUS" in *'"needs_setup":false'*) NEEDS_SETUP=0 ;; esac
    break
  }
  tries=$((tries + 1))
  sleep 1
done
if [ "$NEEDS_SETUP" = "1" ]; then
  echo "  Setup token: journalctl -u windlass | grep setup_token"
  echo
  echo "  The one-time setup token in the server log claims this instance."
else
  echo "  This instance is already claimed: sign in with your existing account."
fi

CLOSED=""
port_closed 80 http && CLOSED="80"
port_closed 443 https && CLOSED="${CLOSED:+$CLOSED and }443"
if [ "$WANT_CADDY" = "1" ] && [ -n "$CLOSED" ]; then
  echo
  case "$CLOSED" in
    *" and "*) warn "Ports $CLOSED look closed by $FIREWALL. Domains and HTTPS need 80 and 443 open." ;;
    *) warn "Port $CLOSED looks closed by $FIREWALL. Domains and HTTPS need 80 and 443 open." ;;
  esac
  if [ "$FIREWALL" = "ufw" ]; then
    echo "    sudo ufw allow 80/tcp && sudo ufw allow 443/tcp"
  else
    echo "    sudo firewall-cmd --permanent --add-service=http --add-service=https && sudo firewall-cmd --reload"
  fi
fi
