package replay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
)

// TickResult is the canonical serialization of one evaluation tick's complete
// deterministic output — the unit the replay guarantee is stated over.
// Cascades are part of it (doc 07 §3.4/M5): recognition is windowed over the
// tick SEQUENCE, and replay reproduces the sequence, so the stories must
// reproduce byte-identically too.
type TickResult struct {
	EvalNow      time.Time             `json:"eval_now"`
	Fingerprints []observe.Fingerprint `json:"fingerprints"`
	Findings     []detect.Finding      `json:"findings"`
	Cascades     []detect.Cascade      `json:"cascades"`
}

// Digest computes the canonical digest of one tick's output: sha256 over the
// canonical JSON. The same digest function runs live (stamped into the tick
// frame) and in replay (recomputed from disk); equality IS the byte-identity
// guarantee. Nil slices are canonicalized to empty so a no-binding live tick and
// its replay agree on the bytes. The canonical bytes are returned for diffing.
//
// The error case is real, not theoretical: encoding/json refuses non-finite
// floats, so a NaN/Inf that somehow reached a fingerprint (the ingest gate drops
// them; this is defence in depth) surfaces as an error — handled by the caller
// (live: tick stated as uncapturable; replay: the run fails loudly) — never a
// panic in a long-running daemon and never a silently wrong digest.
func Digest(evalNow time.Time, fps []observe.Fingerprint, findings []detect.Finding, cascades []detect.Cascade) (string, []byte, error) {
	if fps == nil {
		fps = []observe.Fingerprint{}
	}
	if findings == nil {
		findings = []detect.Finding{}
	}
	if cascades == nil {
		cascades = []detect.Cascade{}
	}
	canonical, err := json.Marshal(TickResult{EvalNow: evalNow, Fingerprints: fps, Findings: findings, Cascades: cascades})
	if err != nil {
		return "", nil, fmt.Errorf("replay: canonical encoding: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), canonical, nil
}

// TickRecord is the payload of a warm-segment tick frame: the evaluation
// instant, which bars epoch was in force, the topology snapshot the matcher
// walked (doc 07 §3.7: same readings + same graph version + same TOPOLOGY
// snapshot ⇒ same matches), and the live digest to verify against. Topology is
// per-tick, not per-epoch: confirmation stamps advance continuously and
// suspicion is a stamp-relative judgement, so the snapshot actually evaluated
// IS the input — recording anything coarser would replay a different topology
// than live saw. nil = a tick captured before topology recording (replayed in
// entity-local-only mode, stated); empty non-nil = a genuinely edgeless tick.
type TickRecord struct {
	EvalNow      time.Time           `json:"eval_now"`
	BarsEpoch    int                 `json:"bars_epoch"`
	Topology     []identity.EdgeSnap `json:"topology"`
	Digest       string              `json:"digest"`
	Fingerprints int                 `json:"fingerprints"`
	Findings     int                 `json:"findings"`
}
