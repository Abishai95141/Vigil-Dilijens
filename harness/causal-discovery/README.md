# causal-discovery — offline PCMCI pruning of obsd associations (doc 29 Part B)

PoC of the charter-clean plan: take the lag-0 associations obsd surfaces (/api/dependency),
and PRUNE them into a small, confounder-aware, lag-annotated CANDIDATE shortlist a human
adjudicates — WITHOUT auto-asserting direction.

Pipeline:  capture panel -> lag-0 graph (the "before", == obsd assoc)
        -> Stage 1 fixed, DECLARED-lag enrichment (NOT argmax-as-direction)
        -> Stage 2 PCMCI+ conditional-independence pruning (tigramite ParCorr)
        -> ranked candidate shortlist (direction = PROJECTED hint; operator authors).

## Run
    uv venv --python 3.12 && uv pip install --python .venv tigramite scikit-learn numpy scipy pandas
    # bring up abb cluster (doc 24) + obsd; then:
    bash run_panel.sh             # ~15 min: captures the pipeline panel while STEPPING gateway load
    .venv/bin/python causal.py    # before/after graphs + confounder audit + ranked shortlist JSON

## Files
- capture.py    cAdvisor (API-server proxy, incl. pod-level network) + app /metrics -> long CSV
- causal.py     the harness (panel build, lag-0, fixed-lag, PCMCI+, confounder audit, shortlist)
- run_panel.sh  captures the pipeline while stepping the gateway LOAD (a common driver)
- *.sample.*    a recorded run + its emitted candidate shortlist

## PoC result (doc 29)
- BEFORE 26 cross-entity lag-0 edges -> AFTER 6 directed candidates (77% fewer).
- 7/7 spurious common-driver edges (e.g. gateway.cpu~stream.cpu r=.53 -> .27|driver) PRUNED.
- PCMCI deterministic (run1==run2), 445ms/12 streams. Direction unreliable at 15s bins
  (pipeline propagates < 1 bin -> contemporaneous) => stays a PROJECTED hint the operator
  authors (charter-aligned). Finer sampling / slower (integrator) signals resolve lags better.
