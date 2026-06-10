package clock

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	clockv1 "github.com/Abishai95141/Vigil-Dilijens/proto/gen/go/vigil/clock/v1"
)

// TestClockProtoHasNoStringFields enforces the charter invariant (doc 01 §4,
// techstack §5): the clock service carries NO string fields anywhere. The model
// never sees labels, names, units, or graph structure — only floats and ints.
//
// This is a compile-/test-time property, not a code-review convention: if anyone
// adds a string (or bytes) field to clock.proto, this test fails. The two permitted
// graph->model flows (target selection, footprint subtraction) both happen OUTSIDE
// this boundary; nothing semantic crosses it.
func TestClockProtoHasNoStringFields(t *testing.T) {
	fd := clockv1.File_vigil_clock_v1_clock_proto
	if fd == nil {
		t.Fatal("clock file descriptor is nil — generated code missing? run `just gen`")
	}

	var offenders []string
	var walk func(prefix string, msgs protoreflect.MessageDescriptors)
	walk = func(prefix string, msgs protoreflect.MessageDescriptors) {
		for i := 0; i < msgs.Len(); i++ {
			md := msgs.Get(i)
			name := prefix + string(md.Name())
			fields := md.Fields()
			for j := 0; j < fields.Len(); j++ {
				f := fields.Get(j)
				switch f.Kind() {
				case protoreflect.StringKind, protoreflect.BytesKind:
					// bytes is barred too: it is the obvious smuggling channel for
					// a label encoded as raw bytes.
					offenders = append(offenders, name+"."+string(f.Name())+" ("+f.Kind().String()+")")
				}
			}
			walk(name+".", md.Messages()) // recurse into nested message types
		}
	}
	walk("", fd.Messages())

	if len(offenders) > 0 {
		t.Fatalf("clock proto must carry NO string/bytes fields (charter, doc 01 §4); found: %v", offenders)
	}
}

// TestClockServiceShape is a light sanity check that the contract surface exists as
// the client expects, so a bad regeneration is caught early.
func TestClockServiceShape(t *testing.T) {
	fd := clockv1.File_vigil_clock_v1_clock_proto
	svcs := fd.Services()
	if svcs.Len() != 1 {
		t.Fatalf("expected exactly 1 service, got %d", svcs.Len())
	}
	svc := svcs.Get(0)
	if got := string(svc.Name()); got != "ForecastService" {
		t.Errorf("service name = %q, want ForecastService", got)
	}
	for _, want := range []string{"Forecast", "Health"} {
		if svc.Methods().ByName(protoreflect.Name(want)) == nil {
			t.Errorf("missing RPC method %q", want)
		}
	}
}
