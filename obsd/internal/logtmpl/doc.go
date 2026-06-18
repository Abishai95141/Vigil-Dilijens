// Package logtmpl mines log TEMPLATES from raw log lines (doc 20 P4, the log lane):
// a Go-native, deterministic Drain (fixed-depth parse tree) that turns an unbounded
// stream of log lines into a bounded set of (template, count) pairs. A mined template
// is MEASURED — a deterministic arithmetic consequence of the byte stream, the same
// class as a fingerprint. It is NOT an authored event and NOT a cause.
//
// WHY GO-NATIVE (not Drain3 the Python library). Drain3 is excellent, but its parse
// tree is INPUT-ORDER-DEPENDENT and its state must be snapshotted to replay — the two
// determinism hazards the design review flagged HIGH. A Go reimplementation gives full
// control: Mine SORTS its input first (a deterministic total order), so the template
// set is byte-identical across runs AND invariant to the order lines arrive in. Regex
// is the AUTHORED first layer (operator patterns map known lines to known events);
// this miner is the MEASURED second layer for the unmapped tail; an LLM name/grouping
// for a template is a PROPOSED candidate (doc 20 §3.1), never an authored fact.
//
// DETERMINISM (the logtmpl-gate). Mine is a pure function: same lines + same params ⇒
// byte-identical []Template. It pins every order — input sort, masking, fixed-depth
// tree navigation, and a final sort of the output by template — and uses only DECLARED
// (never data-fit) params (tree depth, similarity threshold, max children).
//
// This lane is OFF the deterministic fingerprint digest (logs are sampled/unbounded,
// like the events lane): mining never perturbs detection or replay.
package logtmpl
