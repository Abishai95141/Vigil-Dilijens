#!/usr/bin/env bash
# Fault-cascade capture for DIRECTED lead-lag recovery. Oscillate stream-processor latency
# (a square wave) so a clear, SLOW, LAGGED chain propagates: latency -> stream.queue (fast) ->
# datalake writes fall (lagged) -> asset-api staleness rises (more lagged, an integrator).
# Fine 5s capture of the app metrics (which update per sim-cycle) so the lags are RESOLVABLE.
set -u
cd /tmp/vigil_exp
NS=abb-genix
RUN=/tmp/vigil_exp/cascade.csv
SCHED=/tmp/vigil_exp/cascade_schedule.csv
LOG=/tmp/vigil_exp/cascade.log
ctl(){ kubectl -n "$NS" exec "deploy/$1" -- python3 -c "from urllib.request import urlopen; urlopen('http://localhost:8080/ctl?$2', timeout=5)" >/dev/null 2>&1; }
stamp(){ echo "$(date +%s),$1,$2" >> "$SCHED"; echo "[$(date +%H:%M:%S)] $1 $2" | tee -a "$LOG"; }

echo "ts,event,detail" > "$SCHED"; : > "$LOG"
ctl opcua-gateway "rate=30"        # warm steady flow so the queue actually builds
ctl stream-processor "slow=0"
.venv/bin/python capture.py --interval 5 --out "$RUN" \
  --app-deploys smart-sensors,opcua-gateway,stream-processor,pdm-analyzer,asset-api,operations-dashboard \
  --cadvisor-metrics container_cpu_usage_seconds_total,container_memory_working_set_bytes \
  >/tmp/vigil_exp/cascade_capture.log 2>&1 &
CAP=$!
stamp start "rate=30 cap=$CAP"

sleep 60                            # clean baseline
for i in 1 2 3 4; do
  stamp latency_on "stream slow=500"; ctl stream-processor "slow=500"
  sleep 75
  stamp latency_off "stream slow=0"; ctl stream-processor "slow=0"
  sleep 75
done
sleep 20
stamp stop "killing capture"; kill -INT "$CAP" 2>/dev/null; sleep 3
ctl stream-processor "slow=0"; ctl opcua-gateway "rate=5"
echo "[$(date +%H:%M:%S)] DONE rows=$(wc -l < "$RUN")" | tee -a "$LOG"
