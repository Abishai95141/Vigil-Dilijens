package replay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
)

// TickResult is the canonical serialization of one evaluation tick's complete
// deterministic output — the unit the replay guarantee is stated over.
type TickResult struct {
	EvalNow      time.Time             `json:"eval_now"`
	Fingerprints []observe.Fingerprint `json:"fingerprints"`
	Findings     []detect.Finding      `json:"findings"`
}

// Digest computes the canonical digest of one tick's output: sha256 over the
// canonical JSON. The same digest function runs live (stamped into the tick
// frame) and in replay (recomputed from disk); equality IS the byte-identity
// guarantee. Nil slices are canonicalized to empty so a no-binding live tick and
// its replay agree on the bytes. The canonical bytes are returned for diffing.
func Digest(evalNow time.Time, fps []observe.Fingerprint, findings []detect.Finding) (string, []byte) {
	if fps == nil {
		fps = []observe.Fingerprint{}
	}
	if findings == nil {
		findings = []detect.Finding{}
	}
	canonical, err := json.Marshal(TickResult{EvalNow: evalNow, Fingerprints: fps, Findings: findings})
	if err != nil {
		// Marshal of these concrete types cannot fail (no channels/funcs/cycles);
		// if it ever does, the digest must not silently pass as some default.
		panic("replay: canonical marshal: " + err.Error())
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), canonical
}

// TickRecord is the payload of a warm-segment tick frame: the evaluation
// instant, which bars epoch was in force, and the live digest to verify against.
type TickRecord struct {
	EvalNow      time.Time `json:"eval_now"`
	BarsEpoch    int       `json:"bars_epoch"`
	Digest       string    `json:"digest"`
	Fingerprints int       `json:"fingerprints"`
	Findings     int       `json:"findings"`
}
