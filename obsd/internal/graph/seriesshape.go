// Series-shape normalization of the KG's free-text data_type — the canonical
// parser lives in the module-root internal/seriesshape (shared with
// tools/graphlint so the runtime loader and the ontology linter can never
// drift apart); this file binds it onto the loaded Signal.
package graph

import "github.com/Abishai95141/Vigil-Dilijens/internal/seriesshape"

// SeriesShape is the canonical classification of one signal's data_type
// (alias of the shared parser's type — see internal/seriesshape).
type SeriesShape = seriesshape.Shape

// ParseSeriesShape classifies one free-text data_type spelling (shared
// implementation — see internal/seriesshape).
func ParseSeriesShape(dataType string) SeriesShape { return seriesshape.Parse(dataType) }

// Shape returns the signal's canonical series shape, derived from its authored
// data_type at call time (pure function of the verbatim text).
func (s *Signal) Shape() SeriesShape { return ParseSeriesShape(s.DataType) }
