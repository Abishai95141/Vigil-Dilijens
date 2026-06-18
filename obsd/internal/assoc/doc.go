// Package assoc computes a MEASURED metric-dependency graph (doc 20 P2): which
// observed series move together, as windowed associations labelled
// `associated-with` — never `causes`. A correlation is a deterministic arithmetic
// consequence of the readings (like a rate or a co-occurrence), so it is MEASURED,
// not learned: there is no fitted weight, no optimization, no model.
//
// CHARTER POSITION (doc 01 + doc 20 §2.3). This is association, NOT causation. The
// edges are deliberately SYMMETRIC (undirected) — no lead-lag, no direction — so they
// cannot be misread as a causal arrow. Only an AUTHORED phenomenon relation
// legitimizes a causal direction; an assoc edge is a candidate input for that
// authoring, never the claim itself.
//
// THE FIREWALL (doc 20 §2.3, the critic's HIGH flag). assoc is OFF the deterministic
// path and MUST NOT feed it: routing a fitted/observed coefficient into forecast
// selection (selection/tierb) or footprint subtraction (forecast/decompose) would
// launder a data-derived number onto the replay-load-bearing path. firewall_test.go
// asserts no deterministic package imports internal/assoc. The surfacing layer reads
// assoc (via a mapped view), never the engine.
//
// DETERMINISM (the assoc-gate). Associate is a pure function: same series + same
// params ⇒ byte-identical edge set. It pins every order that float arithmetic depends
// on — sorted streams, sorted overlapping bins, fixed-order summation — and a declared
// (never data-fit) overlap floor and coefficient floor. assoc_test.go is the gate:
// the result is identical across runs and invariant to input-sample order.
package assoc
