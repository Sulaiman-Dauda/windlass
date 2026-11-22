#!/bin/sh
# Times one panel's official installer on a fresh Ubuntu 24.04 server. Run as root.
# Usage: install.sh windlass|coolify|dokploy
# Writes /root/install.log and, when finished, /root/install.done with
# "panel=... exit=... seconds=...".
set -u
panel=${1:?panel}
case "$panel" in
  # --yes because the installer asks before installing Docker and there is no terminal.
  windlass) cmd='curl -fsSL https://get.windlass.run | sh -s -- --yes' ;;
  coolify)  cmd='curl -fsSL https://cdn.coollabs.io/coolify/install.sh | bash' ;;
  dokploy)  cmd='curl -sSL https://dokploy.com/install.sh | sh' ;;
  *) echo "unknown panel $panel" >&2; exit 2 ;;
esac
rm -f /root/install.done
start=$(date +%s)
sh -c "$cmd" > /root/install.log 2>&1
rc=$?
end=$(date +%s)
echo "panel=$panel exit=$rc seconds=$((end - start))" > /root/install.done
