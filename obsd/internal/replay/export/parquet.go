// Package export bridges replay bundles to the Python harness (doc 14 §2.3
// "Bundle export"): sealed qss segments export to Parquet so the backtest and
// falsification analytics read columnar data with zero custom parsing. It lives
// in its own package so only cmd/replay links the Parquet encoder — obsd's
// runtime binary stays free of it.
package export

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/parquet-go/parquet-go"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
)

// Reading is one exported sample row. Times are UTC unix nanoseconds — exact,
// timezone-free, and directly usable by pyarrow/polars.
type Reading struct {
	StreamID     string  `parquet:"stream_id"`
	CEIKey       string  `parquet:"cei_key"`
	UID          string  `parquet:"uid"`
	Kind         string  `parquet:"kind"`
	Metric       string  `parquet:"metric"`
	Type         string  `parquet:"type"`
	Node         string  `parquet:"node"`
	RecvUnixNano int64   `parquet:"recv_unix_nano"`
	AtUnixNano   int64   `parquet:"at_unix_nano"`
	Value        float64 `parquet:"value"`
}

// Parquet exports every sample in the bundle's SEALED segments (in arrival
// order) to one Parquet file. Returns the row count written.
func Parquet(bundleDir, outPath string) (int64, error) {
	segs, err := qss.ListSegments(filepath.Join(bundleDir, "segments"))
	if err != nil {
		return 0, err
	}
	f, err := os.Create(outPath)
	if err != nil {
		return 0, fmt.Errorf("export: %w", err)
	}
	w := parquet.NewGenericWriter[Reading](f)
	var rows int64
	for _, seg := range segs {
		if !seg.Sealed {
			continue // the unsealed tail is not part of the bundle (stated by replay)
		}
		idxDef := map[uint32]qss.StreamDef{}
		err := qss.ReplayFrames(seg.Path, func(fr qss.Frame) error {
			switch fr.Kind {
			case qss.FrameDef:
				idxDef[fr.Idx] = fr.Def
			case qss.FrameSample:
				def, ok := idxDef[fr.Idx]
				if !ok {
					return fmt.Errorf("sample references undefined stream index %d", fr.Idx)
				}
				_, werr := w.Write([]Reading{{
					StreamID: def.ID, CEIKey: def.CEIKey, UID: def.UID, Kind: def.Kind,
					Metric: def.Metric, Type: def.Type, Node: def.Node,
					RecvUnixNano: fr.Recv.UnixNano(), AtUnixNano: fr.At.UnixNano(), Value: fr.Value,
				}})
				if werr != nil {
					return fmt.Errorf("export: %w", werr)
				}
				rows++
			}
			return nil
		})
		if err != nil {
			f.Close()
			return rows, err
		}
	}
	if err := w.Close(); err != nil {
		f.Close()
		return rows, fmt.Errorf("export: %w", err)
	}
	if err := f.Close(); err != nil {
		return rows, fmt.Errorf("export: %w", err)
	}
	return rows, nil
}
