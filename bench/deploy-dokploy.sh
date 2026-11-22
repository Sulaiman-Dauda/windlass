#!/bin/sh
# Creates Dokploy's first account and deploys app/compose.yaml as a raw compose project on
# web.<ip>.sslip.io, through the same tRPC API the panel's own pages call. Run as root on
# the server.
set -eu
base=http://127.0.0.1:3000
ip=$(curl -4 -fsS https://api.ipify.org)
host="web.$(echo "$ip" | tr . -).sslip.io"
pass=$(head -c 18 /dev/urandom | base64 | tr -dc A-Za-z0-9)
jar=/root/dokploy.jar
post() { curl -fsS -b $jar -c $jar -H 'Content-Type: application/json' -H "Origin: $base" -X POST "$@"; }
trpc() { post "$base/api/trpc/$1" -d "{\"json\":$2}"; }
get() { python3 -c "import json,sys; d=json.load(sys.stdin)['result']['data']['json']; print(eval(sys.argv[1]))" "$1"; }

post $base/api/auth/sign-up/email \
  -d "{\"email\":\"bench@example.com\",\"password\":\"$pass\",\"name\":\"Bench\",\"lastName\":\"Run\"}" >/dev/null
post $base/api/auth/sign-in/email -d "{\"email\":\"bench@example.com\",\"password\":\"$pass\"}" >/dev/null

env_id=$(trpc project.create '{"name":"bench"}' | get "d['environment']['environmentId']")
compose_file=$(python3 -c 'import json; print(json.dumps(open("/root/compose.yaml").read()))')
compose_id=$(trpc compose.create "{\"name\":\"bench\",\"environmentId\":\"$env_id\",\"composeType\":\"docker-compose\"}" \
  | get "d['composeId']")
trpc compose.update "{\"composeId\":\"$compose_id\",\"sourceType\":\"raw\",\"composeFile\":$compose_file}" >/dev/null
trpc domain.create "{\"host\":\"$host\",\"port\":80,\"https\":false,\"certificateType\":\"none\",\"composeId\":\"$compose_id\",\"serviceName\":\"web\",\"domainType\":\"compose\"}" >/dev/null
trpc compose.deploy "{\"composeId\":\"$compose_id\"}" >/dev/null
echo "$host" > /root/app-host
echo "deployed bench on $host"
