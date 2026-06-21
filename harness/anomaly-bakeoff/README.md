# anomaly-bakeoff — CUSUM vs ADWIN live detector comparison (doc 29)

Live bake-off of Vigil's onset EWMA-CUSUM (faithful port of obsd/internal/onset/onset.go)
vs river ADWIN / PageHinkley, with ruptures as an offline onset oracle, on REAL abb-genix
series with injected faults. See docs/29 for the verdict.

## Run
    uv venv --python 3.12 && uv pip install --python .venv river numpy scipy pandas ruptures statsmodels
    # bring up abb cluster (doc 24) + obsd; then:
    bash run_timeline.sh          # ~8 min: captures real series while injecting faults
    .venv/bin/python analyze.py   # bake-off tables

## Files
- capture.py       cAdvisor (API-server proxy) + app /metrics series capture -> long CSV
- vigil_onset.py   faithful Python port of onset.go (alpha .10 / K .5 / H 4 / Warmup 12 / MinZ 3)
- run_timeline.sh  injection timeline (mem_leak / latency-transient / cpu_burn) w/ epoch ground truth
- analyze.py       Vigil-CUSUM vs ADWIN vs Page-Hinkley + ruptures oracle; latency/FP/localization/determinism
- *.sample.csv     a recorded run + its ground-truth schedule (the numbers in doc 29)

Finding: ADWIN (drift detector) lags gentle creeps ~10x and misses noisy real steps at
every delta; neither flags 1-sample spikes. CUSUM stays for onset timing + forecast
activation; ADWIN at most a complementary off-digest regime-shift sensor (pinned delta).
