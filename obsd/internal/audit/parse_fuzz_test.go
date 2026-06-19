package audit

import (
	"strings"
	"testing"
)

// seedFuzzCorpus are bytes that exercise the parser's edges: clean records, every
// dropped stage/verb/code path, partial/truncated JSON, control bytes, huge/empty,
// duplicate auditIDs, and a multi-line blob. Each is fed to ParseEvents as the WHOLE
// JSONL blob (split on newlines), so embedded newlines exercise the line splitter too.
var seedFuzzCorpus = []string{
	"",
	"\n",
	"   \n\t\n   ",
	"{",
	"}",
	"{}",
	"[]",
	"null",
	"not json at all",
	`{ this line is not valid json and must be skipped, never fatal }`,
	`{"auditID":"x"`, // truncated mid-object
	`{"auditID":"x","stage":"ResponseComplete"}`,
	`{"auditID":"x","stage":"ResponseComplete","verb":"create","objectRef":{"name":"n"},"responseStatus":{"code":200}}`,
	`{"auditID":"","stage":"ResponseComplete","verb":"create","objectRef":{"name":"n"},"responseStatus":{"code":200}}`,
	`{"auditID":"x","stage":"RequestReceived","verb":"create","objectRef":{"name":"n"}}`,
	`{"auditID":"x","stage":"ResponseComplete","verb":"get","objectRef":{"name":"n"},"responseStatus":{"code":200}}`,
	`{"auditID":"x","stage":"ResponseComplete","verb":"create","objectRef":{"name":"n"},"responseStatus":{"code":403}}`,
	`{"auditID":"x","stage":"ResponseComplete","verb":"create","objectRef":{"resource":"pods","namespace":"ns","name":"n"},"responseStatus":{"code":200},"stageTimestamp":"2026-06-18T11:00:00Z"}`,
	`{"auditID":"x","stage":"ResponseComplete","verb":"DELETE","objectRef":{"name":"n"},"responseStatus":{"code":201},"stageTimestamp":"not-a-time"}`,
	`{"auditID":"dup","stage":"ResponseComplete","verb":"patch","objectRef":{"name":"a"},"responseStatus":{"code":200}}` + "\n" +
		`{"auditID":"dup","stage":"ResponseComplete","verb":"delete","objectRef":{"name":"a"},"responseStatus":{"code":200}}`,
	`{"auditID":" ","stage":"ResponseComplete","verb":"create","objectRef":{"name":" "},"responseStatus":{"code":299}}`,
	`{"stageTimestamp":1234}`,            // wrong type for a time field
	`{"responseStatus":{"code":"oops"}}`, // wrong type for an int field
	`{"auditID":["arr"],"stage":"ResponseComplete"}`,
	strings.Repeat("{", 1000),
	strings.Repeat(`{"auditID":"x","stage":"ResponseComplete","verb":"create","objectRef":{"name":"n"},"responseStatus":{"code":200}}`+"\n", 50),
}

// FuzzParseEvents asserts the JSONL parser NEVER panics on arbitrary bytes (the live
// audit log is a sampled, externally-written, possibly-truncated file: a partial last
// line, an interrupted write, or a non-audit line must be skipped, never fatal). Beyond
// no-panic, it asserts the parser's OUTPUT INVARIANTS hold for any input — every kept
// ChangeEvent is a real, completed, mutating, named change, and the result is sorted +
// deduped — so a future change that loosens a filter while staying panic-free is caught.
func FuzzParseEvents(f *testing.F) {
	for _, s := range seedFuzzCorpus {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, blob string) {
		// Split the blob into lines exactly as the collector does, then parse. A panic here
		// (slice OOB, nil deref, unbounded recursion) fails the fuzz target.
		lines := strings.Split(blob, "\n")
		got := ParseEvents(lines)

		seen := map[string]bool{}
		for i, ce := range got {
			// Invariant 1: only completed, named changes survive (something to join to).
			if ce.AuditID == "" {
				t.Fatalf("kept a change with an empty auditID: %+v", ce)
			}
			if ce.Name == "" {
				t.Fatalf("kept a change with an empty object name: %+v", ce)
			}
			// Invariant 2: only mutating verbs (reads are never changes).
			if !mutatingVerbs[ce.Verb] {
				t.Fatalf("kept a non-mutating verb %q: %+v", ce.Verb, ce)
			}
			// Invariant 3: the change actually took effect (2xx, or an absent code 0).
			if c := ce.ResponseCode; c != 0 && (c < 200 || c >= 300) {
				t.Fatalf("kept a change that did not take effect (code %d): %+v", c, ce)
			}
			// Invariant 4: deduped by auditID (a fixed winner, never a duplicate row).
			if seen[ce.AuditID] {
				t.Fatalf("duplicate auditID %q survived dedup: %+v", ce.AuditID, ce)
			}
			seen[ce.AuditID] = true
			// Invariant 5: sorted by (timestamp, auditID) — never arrival order.
			if i > 0 {
				prev := got[i-1]
				if ce.Timestamp.Before(prev.Timestamp) {
					t.Fatalf("output not sorted by timestamp at index %d: %v before %v", i, ce.Timestamp, prev.Timestamp)
				}
				if ce.Timestamp.Equal(prev.Timestamp) && ce.AuditID < prev.AuditID {
					t.Fatalf("tie not broken by auditID at index %d: %q < %q", i, ce.AuditID, prev.AuditID)
				}
			}
		}

		// Order invariance must hold for arbitrary inputs too: reversing the (cleaned) lines
		// must not change the parsed output. This folds the determinism guarantee into the
		// fuzzer so a regression that made parsing order-sensitive is found on random data.
		rev := make([]string, len(lines))
		for i := range lines {
			rev[i] = lines[len(lines)-1-i]
		}
		got2 := ParseEvents(rev)
		if len(got) != len(got2) {
			t.Fatalf("parse not order-invariant: len %d vs reversed %d", len(got), len(got2))
		}
		for i := range got {
			if got[i].AuditID != got2[i].AuditID || !got[i].Timestamp.Equal(got2[i].Timestamp) ||
				got[i].Verb != got2[i].Verb || got[i].ResponseCode != got2[i].ResponseCode {
				t.Fatalf("parse not order-invariant at %d: %+v vs %+v", i, got[i], got2[i])
			}
		}
	})
}
