#!/usr/bin/env bash
# Live injection timeline on the abb-genix cluster. Captures real series while injecting
# controlled faults at epoch-stamped times (ground truth). One continuous capture; faults
# on disjoint pods/metrics so arms don't contaminate each other.
set -u
cd /tmp/vigil_exp
NS=abb-genix
RUN=/tmp/vigil_exp/run.csv
SCHED=/tmp/vigil_exp/schedule.csv
LOG=/tmp/vigil_exp/timeline.log

ctl(){ kubectl -n "$NS" exec "deploy/$1" -- python3 -c "from urllib.request import urlopen; urlopen('http://localhost:8080/ctl?$2', timeout=5)" >/dev/null 2>&1; echo "ctl rc=$? $1 ?$2"; }
stamp(){ echo "$(date +%s),$1,$2,$3" >> "$SCHED"; echo "[$(date +%H:%M:%S)] STAMP $1 $2 $3" | tee -a "$LOG"; }

echo "ts,event,target,detail" > "$SCHED"
: > "$LOG"
echo "[$(date +%H:%M:%S)] start; warming steady traffic" | tee -a "$LOG"
ctl opcua-gateway "rate=20" | tee -a "$LOG"
stamp start cluster baseline

# start capture in background (5s cadence); mem+cpu+throttle+cache from cAdvisor, app /metrics
.venv/bin/python capture.py --interval 5 --out "$RUN" \
  --app-deploys pdm-analyzer,stream-processor,asset-api \
  --cadvisor-metrics container_memory_working_set_bytes,container_cpu_usage_seconds_total,container_cpu_cfs_throttled_periods_total,container_memory_cache \
  >/tmp/vigil_exp/capture.log 2>&1 &
CAP=$!
echo "[$(date +%H:%M:%S)] capture pid=$CAP" | tee -a "$LOG"

sleep 90      # 0:00-1:30 STATIONARY baseline
stamp inject mem_leak "pdm-analyzer leak_kb=512 (GRADUAL DRIFT)"
ctl pdm-analyzer "leak=on&leak_kb=512" | tee -a "$LOG"

sleep 90      # ->3:00
stamp inject latency_transient "stream-processor slow=400 (TRANSIENT FP TRAP)"
ctl stream-processor "slow=400" | tee -a "$LOG"

sleep 40      # ->3:40  (40s transient, then settles back)
stamp heal latency_transient "stream-processor slow=0"
ctl stream-processor "slow=0" | tee -a "$LOG"

sleep 80      # ->5:00
stamp inject cpu_burn "stream-processor cpuburn=1 (ABRUPT SUSTAINED STEP)"
ctl stream-processor "cpuburn=1" | tee -a "$LOG"

sleep 120     # ->7:00
stamp heal cpu_burn "stream-processor cpuburn=0"
ctl stream-processor "cpuburn=0" | tee -a "$LOG"

sleep 30      # ->7:30
stamp heal mem_leak "pdm-analyzer leak=off"
ctl pdm-analyzer "leak=off" | tee -a "$LOG"

sleep 40      # ->8:10  capture tail
stamp stop cluster "stopping capture"
kill -INT "$CAP" 2>/dev/null
sleep 3
# restore: heal everything, steady traffic back to a calm baseline
ctl pdm-analyzer "leak=off" | tee -a "$LOG"
ctl stream-processor "slow=0&cpuburn=0" | tee -a "$LOG"
ctl opcua-gateway "rate=5" | tee -a "$LOG"
echo "[$(date +%H:%M:%S)] DONE. rows=$(wc -l < "$RUN")" | tee -a "$LOG"
