#!/bin/sh
# Claims a fresh Windlass, creates project "bench" from app/compose.yaml and puts the web
# service on web.<ip>.sslip.io. Run as root on the server, with compose.yaml in /root.
set -eu
api=http://127.0.0.1:8080/api/v1
ip=$(curl -4 -fsS https://api.ipify.org)
host="web.$(echo "$ip" | tr . -).sslip.io"
# The token changes each time the service starts, so take the latest.
token=$(journalctl -u windlass --no-pager -o cat | grep -o '"setup_token":"[^"]*"' | tail -1 | cut -d'"' -f4)
pass=$(head -c 18 /dev/urandom | base64 | tr -dc A-Za-z0-9)
jar=/root/windlass.jar
j() { python3 -c 'import json,sys; print(json.dumps(dict(zip(sys.argv[1::2], sys.argv[2::2]))))' "$@"; }

curl -fsS -c $jar -H 'Content-Type: application/json' -X POST $api/auth/setup \
  -d "$(j token "$token" email bench@example.com password "$pass")" >/dev/null
curl -fsS -b $jar -H 'Content-Type: application/json' -X POST $api/projects -d '{"name":"bench"}' >/dev/null
curl -fsS -b $jar -H 'Content-Type: application/json' -X PUT $api/projects/bench/files/compose.yaml \
  -d "$(python3 -c 'import json; print(json.dumps({"content": open("/root/compose.yaml").read()}))')"
curl -fsS -b $jar -H 'Content-Type: application/json' -X POST $api/projects/bench/deployments >/dev/null
curl -fsS -b $jar -H 'Content-Type: application/json' -X POST $api/projects/bench/domains \
  -d "{\"hostname\":\"$host\",\"service\":\"web\",\"container_port\":80}" >/dev/null
echo "$host" > /root/app-host
echo "deployed bench on $host"
