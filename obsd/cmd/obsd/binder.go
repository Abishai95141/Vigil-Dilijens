package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"sort"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/kube"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
)

// binder runs the discovery-time binding compilation (doc 04). Binding runs on
// RE-BINDING TRIGGERS, never continuously (doc 04 §3.6): when the identified
// inventory changes (topology-driven entity creation/loss), or on a slow periodic
// re-poll that picks up config drift (limits edited in place). It never gates the
// identity path (doc 01 non-gating: detection-side layers run identically whether
// binding is present or absent).
type binder struct {
	graph    *graph.Graph // nil = binding disabled (stated at startup)
	client   kubernetes.Interface
	logger   *slog.Logger
	ingestor *observe.Ingestor // stream evidence for semantic QA (doc 04 M3)

	last        *binding.Result
	lastFinger  string
	ticksUnseen int
}

// configDriftRebindTicks forces a re-bind every Nth inventory tick even with an
// unchanged inventory, so an in-place config edit (a re-binding trigger we cannot
// watch yet) is picked up within ~1 minute on the dev profile.
const configDriftRebindTicks = 4

// compile returns the current bound customer graph, re-compiling only on a
// trigger. A snapshot/list failure keeps the previous result and says so — a
// stale-but-stated report, never a silent gap.
func (b *binder) compile(ctx context.Context, inventory []identity.InstanceRecord, now time.Time) *binding.Result {
	if b == nil || b.graph == nil {
		return nil
	}
	finger := inventoryFingerprint(inventory)
	if b.last != nil && finger == b.lastFinger && b.ticksUnseen < configDriftRebindTicks {
		b.ticksUnseen++
		return b.last
	}

	snap, err := kube.SnapshotConfig(ctx, b.client)
	if err != nil {
		b.logger.Warn("binding: config snapshot failed; keeping previous bound graph", "err", err)
		return b.last
	}
	res := binding.Compile(b.graph, inventory, snap, now)
	b.last = res
	b.lastFinger = finger
	b.ticksUnseen = 0
	b.logger.Info("bound customer graph compiled",
		"bindings", len(res.Bindings),
		"roles", len(res.Roles),
		"resolvability", res.Coverage.Resolvability,
		"unbounded", len(res.Coverage.UnboundedWorkloads),
		"default_bars", res.Coverage.DefaultBars,
	)
	return res
}

// inventoryFingerprint hashes the sorted active CEI keys: the re-binding trigger
// for topology-driven entity creation/loss.
func inventoryFingerprint(inventory []identity.InstanceRecord) string {
	keys := make([]string, 0, len(inventory))
	for _, r := range inventory {
		keys = append(keys, r.CEI.Key())
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
