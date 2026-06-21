#!/usr/bin/env bash
# Connectivity-loss cascade for DIRECTED lead-lag recovery: oscillate the gateway field link
# (disconnect=1/0). On disconnect the whole pipeline freezes — gateway throughput DROPS first,
# asset-api staleness AGE rises AFTER a lag (the integrator). A clean cross-entity lagged chain.
set -u
cd /tmp/vigil_exp
NS=abb-genix
RUN=/tmp/vigil_exp/cascade2.csv
SCHED=/tmp/vigil_exp/cascade2_schedule.csv
LOG=/tmp/vigil_exp/cascade2.log
ctl(){ kubectl -n "$NS" exec "deploy/$1" -- python3 -c "from urllib.request import urlopen; urlopen('http://localhost:8080/ctl?$2', timeout=5)" >/dev/null 2>&1; }
stamp(){ echo "$(date +%s),$1,$2" >> "$SCHED"; echo "[$(date +%H:%M:%S)] $1 $2" | tee -a "$LOG"; }
echo "ts,event,detail" > "$SCHED"; : > "$LOG"
ctl opcua-gateway "disconnect=0"; ctl opcua-gateway "rate=40"   # warm flow, connected
.venv/bin/python capture.py --interval 5 --out "$RUN" \
  --app-deploys smart-sensors,opcua-gateway,stream-processor,pdm-analyzer,asset-api,operations-dashboard \
  --cadvisor-metrics container_cpu_usage_seconds_total \
  >/tmp/vigil_exp/cascade2_capture.log 2>&1 &
CAP=$!
stamp start "rate=40 cap=$CAP"
sleep 60
for i in 1 2 3 4; do
  stamp disconnect_on "gateway disconnect=1"; ctl opcua-gateway "disconnect=1"
  sleep 75
  stamp disconnect_off "gateway disconnect=0"; ctl opcua-gateway "disconnect=0"
  sleep 75
done
sleep 20
stamp stop "killing capture"; kill -INT "$CAP" 2>/dev/null; sleep 3
ctl opcua-gateway "disconnect=0"; ctl opcua-gateway "rate=5"
echo "[$(date +%H:%M:%S)] DONE rows=$(wc -l < "$RUN")" | tee -a "$LOG"
