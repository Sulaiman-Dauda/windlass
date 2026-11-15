#!/bin/sh
# Deploys app/compose.yaml through Coolify's API as a Docker Compose service on
# web.<ip>.sslip.io. Run as root on the server after the first account was created in the
# browser at :8000/register.
# The API is off by default and tokens are made in the UI; this does both through artisan,
# the same as Settings > Advanced > API Access and Keys & Tokens with the root permission.
set -eu
api=http://127.0.0.1:8000/api/v1
ip=$(curl -4 -fsS https://api.ipify.org)
host="web.$(echo "$ip" | tr . -).sslip.io"
tok=$(docker exec coolify php artisan tinker --execute='
$s = \App\Models\InstanceSettings::get(); $s->is_api_enabled = true; $s->save();
echo \App\Models\User::first()->createToken("bench", ["root"], null, 0)->plainTextToken;' | tail -1)
call() { curl -fsS -H "Authorization: Bearer $tok" -H 'Content-Type: application/json' "$@"; }
field() { python3 -c "import json,sys; d=json.load(sys.stdin); d=d[0] if isinstance(d,list) else d; print(d['$1'])"; }

server=$(call $api/servers | field uuid)
project=$(call -X POST $api/projects -d '{"name":"bench"}' | field uuid)
body=$(python3 - "$project" "$server" "$host" <<'EOF'
import base64, json, sys
project, server, host = sys.argv[1:]
print(json.dumps({
    "project_uuid": project, "server_uuid": server, "environment_name": "production",
    "name": "bench", "instant_deploy": True,
    "docker_compose_raw": base64.b64encode(open("/root/compose.yaml", "rb").read()).decode(),
    "urls": [{"name": "web", "url": f"http://{host}"}],
}))
EOF
)
call -X POST $api/services -d "$body"
echo
echo "$host" > /root/app-host
echo "deployed bench on $host"
