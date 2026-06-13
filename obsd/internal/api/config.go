package api

import "time"

// ConfigView surfaces the runtime configuration an operator needs to read the
// system's posture. It is NOT a provenance-classed statement — it is system
// configuration (like a context window is an annotation, doc 10 M6), so it wears
// no MEASURED/PROJECTED/AUTHORED chip. The fact that matters most here is the
// forecast lane's GATE posture: a forecast class is dark until its backtest
// calibration gate passes (doc 11 §3.5 / 09 M3), and this view states that in
// plain configuration terms — the same honesty the /api/warnings OFF state shows,
// reachable before an operator ever opens the warnings tab.
type ConfigView struct {
	GeneratedAt    time.Time          `json:"generatedAt"`
	ClusterID      string             `json:"clusterId"`
	Profile        string             `json:"profile"`
	ParamsVersion  string             `json:"paramsVersion"`
	GraphRelease   string             `json:"graphRelease"`
	GraphVersion   string             `json:"graphVersion"`
	ScrapeInterval string             `json:"scrapeInterval"`
	EvaluationTick string             `json:"evaluationTick"`
	TierBBudget    int                `json:"tierBBudget"`
	Forecast       ForecastConfigView `json:"forecast"`
	Note           string             `json:"note"`
}

// ForecastConfigView is the "soon" lane's configuration plus its gate posture.
// When Enabled is false the lane produces no operator-visible class; GateNote
// states why (the gate rule). The decomposition settings (09 M5) are shown so an
// operator can see the splice-point regime the forecast context is cleaned by.
type ForecastConfigView struct {
	Enabled              bool    `json:"enabled"`
	GateNote             string  `json:"gateNote"`
	ClockdTarget         string  `json:"clockdTarget"`
	Interval             string  `json:"interval"`
	HorizonSteps         int     `json:"horizonSteps"`
	MinContext           int     `json:"minContext"`
	Decompose            bool    `json:"decompose"`
	ResetDropFraction    float64 `json:"resetDropFraction"`
	MaxExplainedFraction float64 `json:"maxExplainedFraction"`
}

// ForecastGateNote returns the honest OFF note (the gate rule) — exported so the
// runtime can stamp it into a ConfigView without duplicating the wording. When
// the lane is enabled, the caller supplies its own short "on" note instead.
func ForecastGateNote() string { return gateNote }
