package api

import (
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/store"
)

// FindingsView is the MEASURED persisted finding feed surfaced read-only for the MCP
// relay — the same rows /api/findings serves. A row may be STALE (last seen X ago),
// which get_insights (a now-only join) omits; this feed is the place to read "what just
// fired / just cleared". A finding is a MEASURED phenomenon match recorded over time,
// never a cause.
type FindingsView struct {
	Class       string             `json:"class"`
	Available   bool               `json:"available"`
	GeneratedAt time.Time          `json:"generatedAt"`
	Findings    []store.FindingRow `json:"findings"`
}

// BuildFindingsView wraps persisted finding rows as a classed, read-only view and stamps
// each row's serve-time freshness (stale vs firing-now), exactly as /api/findings does. A
// nil slice becomes an empty (honest) feed, never null.
func BuildFindingsView(rows []store.FindingRow, generatedAt time.Time, staleAfter time.Duration) *FindingsView {
	if rows == nil {
		rows = []store.FindingRow{}
	}
	for i := range rows {
		rows[i].MarkFreshness(generatedAt, staleAfter)
	}
	return &FindingsView{Class: "MEASURED", Available: true, GeneratedAt: generatedAt, Findings: rows}
}
