package forecast

import (
	"reflect"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
)

var roleBase = time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)

func sm(offsetSteps int, v float64) qss.Sample {
	return qss.Sample{At: roleBase.Add(time.Duration(offsetSteps) * 15 * time.Second), Value: v}
}

// THE CHURN TEST (doc 20 P5): pod A is replaced by pod B mid-window. The role series
// must be CONTINUOUS across the handoff — that is the whole point of OwnerReference
// succession. A per-pod forecast would see two broken half-series; the role series is
// one unbroken curve.
func TestAggregateRoleSeriesFollowsChurn(t *testing.T) {
	members := map[string][]qss.Sample{
		"podA": {sm(0, 10), sm(1, 20), sm(2, 30)}, // active bins 0..2, then replaced
		"podB": {sm(3, 40), sm(4, 50), sm(5, 60)}, // its successor, bins 3..5
	}
	start, end := roleBase, roleBase.Add(6*15*time.Second)
	got := AggregateRoleSeries(members, start, end, 15*time.Second)

	want := []qss.Sample{sm(0, 10), sm(1, 20), sm(2, 30), sm(3, 40), sm(4, 50), sm(5, 60)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("role series not continuous across churn:\n got %+v\nwant %+v", got, want)
	}
}

func TestAggregateRoleSeriesSumsOverlapAndIsOrderInvariant(t *testing.T) {
	// both pods alive in the same bins ⇒ the role series is their SUM per bin.
	members := map[string][]qss.Sample{
		"podA": {sm(0, 10), sm(1, 20)},
		"podB": {sm(0, 1), sm(1, 2)},
	}
	start, end := roleBase, roleBase.Add(2*15*time.Second)
	bin := 15 * time.Second
	got := AggregateRoleSeries(members, start, end, bin)
	want := []qss.Sample{sm(0, 11), sm(1, 22)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("per-bin sum wrong: got %+v want %+v", got, want)
	}
	// shuffle each member's samples — the aggregation must be identical (it sorts).
	shuffled := map[string][]qss.Sample{
		"podB": {sm(1, 2), sm(0, 1)},
		"podA": {sm(1, 20), sm(0, 10)},
	}
	if got2 := AggregateRoleSeries(shuffled, start, end, bin); !reflect.DeepEqual(got, got2) {
		t.Fatalf("aggregation is not order-invariant:\n%+v\n%+v", got, got2)
	}
}

func TestRoleSeriesReaderAdapter(t *testing.T) {
	// reuses the package's fakeReader (forecast_test.go).
	base := &fakeReader{
		streams: map[string][]string{
			"podA\x1fmem": {"sA"},
			"podB\x1fmem": {"sB"},
		},
		samples: map[string][]qss.Sample{
			"sA": {sm(0, 10), sm(1, 20), sm(2, 30)},
			"sB": {sm(3, 40), sm(4, 50), sm(5, 60)},
		},
		types: map[string]string{"sA": "gauge", "sB": "gauge"},
	}
	r := &RoleSeriesReader{
		Base: base,
		Members: func(role string) []string {
			if role == "R" {
				return []string{"podA", "podB"}
			}
			return nil
		},
		Bin:    15 * time.Second,
		Window: 10 * time.Minute,
		Now:    func() time.Time { return roleBase.Add(6 * 15 * time.Second) },
	}

	// a role uid with live members ⇒ one synthetic role stream.
	ids := r.StreamsFor("R", "mem")
	if len(ids) != 1 || ids[0][:len(RoleStreamPrefix)] != RoleStreamPrefix {
		t.Fatalf("role StreamsFor wrong: %v", ids)
	}
	// LastN aggregates the members into one continuous churn-stable series.
	got := r.LastN(ids[0], 1024)
	want := []qss.Sample{sm(0, 10), sm(1, 20), sm(2, 30), sm(3, 40), sm(4, 50), sm(5, 60)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("adapter LastN aggregation wrong:\n got %+v\nwant %+v", got, want)
	}
	if tp, ok := r.StreamType(ids[0]); !ok || tp != "gauge" {
		t.Errorf("role StreamType = %q,%v, want gauge,true", tp, ok)
	}
	// a NON-role uid delegates unchanged (byte-identical to the base).
	if d := r.StreamsFor("podA", "mem"); !reflect.DeepEqual(d, []string{"sA"}) {
		t.Errorf("non-role StreamsFor must delegate, got %v", d)
	}
	if d := r.LastN("sA", 2); !reflect.DeepEqual(d, []qss.Sample{sm(1, 20), sm(2, 30)}) {
		t.Errorf("non-role LastN must delegate, got %v", d)
	}
	// an unknown role with no members delegates (honest no-stream, never invented).
	if ids := r.StreamsFor("unknown-role", "mem"); ids != nil && len(ids) != 0 {
		t.Errorf("unknown role must not invent a stream, got %v", ids)
	}
}

// TestRoleSeriesReaderContainerStream is the regression for the doc 20 P5 short-context
// bug: a CONTAINER target rolls up to a StreamUID of "roleKey\x1fcontainer", so the
// synthetic role stream id has THREE \x1f parts (roleKey, container, metric). parseRoleStream
// must split on the LAST separator and hand the full "roleKey\x1fcontainer" uid back to
// Members; splitting on the first one drops the container, no member stream resolves, and
// the aggregation is empty (the spurious short-context that silenced every container metric).
func TestRoleSeriesReaderContainerStream(t *testing.T) {
	base := &fakeReader{
		streams: map[string][]string{
			"podA/ctr\x1fmem": {"sA"},
			"podB/ctr\x1fmem": {"sB"},
		},
		samples: map[string][]qss.Sample{
			"sA": {sm(0, 10), sm(1, 20), sm(2, 30)},
			"sB": {sm(3, 40), sm(4, 50), sm(5, 60)},
		},
		types: map[string]string{"sA": "gauge", "sB": "gauge"},
	}
	r := &RoleSeriesReader{
		Base: base,
		// Container-aware resolver (mirrors main.buildRoleMembers): a uid with a
		// container component yields podUID/container members.
		Members: func(roleUID string) []string {
			switch roleUID {
			case "R\x1fctr":
				return []string{"podA/ctr", "podB/ctr"}
			case "R":
				return []string{"podA", "podB"}
			}
			return nil
		},
		Bin:    15 * time.Second,
		Window: 10 * time.Minute,
		Now:    func() time.Time { return roleBase.Add(6 * 15 * time.Second) },
	}

	ids := r.StreamsFor("R\x1fctr", "mem")
	if len(ids) != 1 || ids[0][:len(RoleStreamPrefix)] != RoleStreamPrefix {
		t.Fatalf("container-role StreamsFor wrong: %v", ids)
	}
	// The fix: LastN must reconstruct podUID/container members and aggregate them — not
	// return empty (the bug → short-context). want = the continuous churn-stable series.
	got := r.LastN(ids[0], 1024)
	want := []qss.Sample{sm(0, 10), sm(1, 20), sm(2, 30), sm(3, 40), sm(4, 50), sm(5, 60)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("container-role LastN aggregation wrong (regression):\n got %+v\nwant %+v", got, want)
	}
	if tp, ok := r.StreamType(ids[0]); !ok || tp != "gauge" {
		t.Errorf("container-role StreamType = %q,%v, want gauge,true", tp, ok)
	}
}
