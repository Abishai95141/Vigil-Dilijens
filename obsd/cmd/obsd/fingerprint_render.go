package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
)

// renderFingerprints writes the live per-entity fingerprints (doc 05 §3.4) to w —
// the first MEASURED "what is happening now": each thresholded variable on its
// ladder against the live bar, each rate guard, with the stream + bar each derived
// from. Crossed entities are surfaced first (that is what detection screens on).
// Bar provenance rides along: a default-sourced rung is flagged, never silently
// carrying config authority.
func renderFingerprints(w io.Writer, fps []observe.Fingerprint, focusNamespace string) {
	const rule = "--------------------------------------------------------------------------------"
	fmt.Fprintln(w, rule)

	var crossed, vars, stale int
	for _, fp := range fps {
		if fp.Crossed() {
			crossed++
		}
		for _, t := range fp.Thresholds {
			vars++
			if t.Stale {
				stale++
			}
		}
		vars += len(fp.Rates)
	}
	fmt.Fprintf(w, " live fingerprints (doc 05) — %d entities · %d variables evaluated · %d crossing · %d stale\n",
		len(fps), vars, crossed, stale)

	// Order: crossed entities first, then by namespace/name; within the focus
	// namespace show all, others only if crossing (keep the demo legible while
	// staying honest about cluster-wide evaluation).
	ordered := append([]observe.Fingerprint(nil), fps...)
	sort.SliceStable(ordered, func(i, j int) bool {
		ci, cj := ordered[i].Crossed(), ordered[j].Crossed()
		if ci != cj {
			return ci // crossing first
		}
		if ordered[i].Namespace != ordered[j].Namespace {
			return ordered[i].Namespace < ordered[j].Namespace
		}
		return ordered[i].Name < ordered[j].Name
	})

	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "  ENTITY\tVARIABLE\tLADDER\tVALUE\tBAR\tPROVENANCE")
	shown := 0
	for _, fp := range ordered {
		focus := fp.Namespace == focusNamespace
		if !focus && !fp.Crossed() {
			continue // off-focus and quiet: omit the row, counted in the header
		}
		shown++
		ent := fp.Name
		if fp.Namespace != "" {
			ent = fp.Namespace + "/" + fp.Name
		}
		for _, t := range fp.Thresholds {
			flags := provenance(t.BarSource, t.Flagged, t.Stale, t.Deriv.How)
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s %s\t%s\n",
				ent, shortMetric(t.Metric), t.State, fmtVal(t.Value, t.Unit), t.Direction, fmtVal(t.Bar, t.Unit), flags)
		}
		for _, r := range fp.Rates {
			state := "ok"
			switch {
			case r.Inconclusive:
				state = "inconclusive"
			case r.Breached:
				state = "BREACHED"
			}
			flags := provenance(r.BarSource, r.Flagged, r.Stale, r.Deriv.How)
			if r.GapBroken {
				flags += ",gap-broken"
			}
			if r.Resets > 0 {
				flags += fmt.Sprintf(",resets=%d", r.Resets)
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%.0f/window\t>= %.0f\t%s\n",
				ent, shortMetric(r.Metric), state, r.WindowDelta, r.Bar, flags)
		}
	}
	_ = tw.Flush()
	if shown == 0 {
		fmt.Fprintln(w, "  (no crossings; all evaluated variables on the healthy side of their bars)")
	}
	fmt.Fprintln(w, rule)
}

// provenance renders the honesty flags that ride with every fingerprint component.
func provenance(barSource string, flagged, stale bool, how string) string {
	parts := []string{how}
	if flagged {
		parts = append(parts, "default-flagged")
	} else {
		parts = append(parts, barSource)
	}
	if stale {
		parts = append(parts, "STALE")
	}
	return strings.Join(parts, ",")
}

// shortMetric trims long Prometheus names for the table.
func shortMetric(m string) string {
	m = strings.TrimPrefix(m, "container_")
	m = strings.TrimSuffix(m, "_total")
	return m
}

// fmtVal renders a value in its unit compactly.
func fmtVal(v float64, unit string) string {
	switch unit {
	case "bytes":
		return humanBytes(v)
	case "millicores":
		return fmt.Sprintf("%.0fm", v)
	case "ratio":
		return fmt.Sprintf("%.3f", v)
	default:
		return fmt.Sprintf("%.2f", v)
	}
}
