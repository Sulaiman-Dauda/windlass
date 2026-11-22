#!/bin/sh
# Memory and disk samples for the panel benchmark. Run as root on the server.
# Usage: measure.sh STAGE [SAMPLES] [GAP_SECONDS]
# Prints CSV lines: time,stage,metric,value (MiB unless the metric says otherwise).
set -u
stage=${1:?stage}
n=${2:-10}
gap=${3:-30}

mib() { awk '{
  v=$1; u=v; sub(/^[0-9.]+/,"",u); sub(/[A-Za-z]+$/,"",v)
  f=1; if(u=="B")f=1/1048576; if(u=="KiB"||u=="kB")f=1/1024; if(u=="GiB"||u=="GB")f=1024
  printf "%.1f", v*f }'; }

i=1
while [ "$i" -le "$n" ]; do
  t=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  awk -v t="$t" -v s="$stage" '
    /^MemTotal:/{tot=$2} /^MemAvailable:/{av=$2} /^SwapTotal:/{st=$2} /^SwapFree:/{sf=$2}
    END{printf "%s,%s,used,%.1f\n%s,%s,total,%.1f\n%s,%s,swap_used,%.1f\n",
      t,s,(tot-av)/1024,t,s,tot/1024,t,s,(st-sf)/1024}' /proc/meminfo
  # Host processes, summed by command name.
  ps -eo comm=,rss= | awk -v t="$t" -v s="$stage" '
    { c=$1; if (c ~ /^containerd-shim/) c="containerd-shim"; r[c]+=$2 }
    END{ split("dockerd containerd containerd-shim docker-proxy caddy windlass", k, " ")
      for (j in k) printf "%s,%s,proc:%s,%.1f\n", t, s, k[j], r[k[j]]/1024 }'
  if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    docker stats --no-stream --format '{{.Name}} {{.MemUsage}}' | while read -r name use _; do
      printf '%s,%s,ctr:%s,%s\n' "$t" "$stage" "$name" "$(echo "$use" | mib)"
    done
    printf '%s,%s,containers,%s\n' "$t" "$stage" "$(docker ps -q | wc -l)"
  fi
  printf '%s,%s,disk_used,%s\n' "$t" "$stage" "$(df -BM --output=used / | tail -1 | tr -dc 0-9)"
  i=$((i + 1))
  [ "$i" -le "$n" ] && sleep "$gap"
done
