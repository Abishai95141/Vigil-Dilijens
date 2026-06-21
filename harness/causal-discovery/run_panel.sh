#!/usr/bin/env bash
# Capture a multivariate panel of the abb pipeline while stepping the gateway LOAD (a common
# driver that propagates down smart-sensors->gateway->broker->stream->historian->datalake with
# lags). This gives PCMCI real lagged causality to recover AND confounded co-movements to prune.
set -u
cd /tmp/vigil_exp
NS=abb-genix
RUN=/tmp/vigil_exp/panel.csv
SCHED=/tmp/vigil_exp/panel_schedule.csv
LOG=/tmp/vigil_exp/panel.log
ctl(){ kubectl -n "$NS" exec "deploy/$1" -- python3 -c "from urllib.request import urlopen; urlopen('http://localhost:8080/ctl?$2', timeout=5)" >/dev/null 2>&1; }
stamp(){ echo "$(date +%s),$1,$2" >> "$SCHED"; echo "[$(date +%H:%M:%S)] $1 $2" | tee -a "$LOG"; }

echo "ts,event,detail" > "$SCHED"; : > "$LOG"
ctl opcua-gateway "rate=10"
.venv/bin/python capture.py --interval 10 --out "$RUN" \
  --app-deploys smart-sensors,opcua-gateway,stream-processor,pdm-analyzer,asset-api,operations-dashboard \
  --cadvisor-metrics container_cpu_usage_seconds_total,container_network_receive_bytes_total,container_network_transmit_bytes_total,container_memory_working_set_bytes,container_file_descriptors \
  >/tmp/vigil_exp/panel_capture.log 2>&1 &
CAP=$!
stamp start "rate=10 cap=$CAP"

# step the common driver (gateway forward rate) to create propagating, lagged dynamics
for r in 50 15 70 25 60 12 45 20 65 30 10; do
  sleep 80; ctl opcua-gateway "rate=$r"; stamp rate "$r"
done
sleep 40
stamp stop "killing capture"
kill -INT "$CAP" 2>/dev/null; sleep 3
ctl opcua-gateway "rate=5"
echo "[$(date +%H:%M:%S)] DONE rows=$(wc -l < "$RUN")" | tee -a "$LOG"
