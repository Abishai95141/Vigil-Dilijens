package clock

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	clockv1 "github.com/Abishai95141/Vigil-Dilijens/proto/gen/go/vigil/clock/v1"
)

// Client is the Go side of the clock contract (doc 09 §3.1): one bare float
// series in, a point trajectory plus a quantile band out. Nothing semantic
// crosses this boundary in either direction — the proto carries no string
// fields (conformance_test.go) and this client adds none.
//
// NON-GATING (doc 09 §2, absolute): callers own the deadline via ctx. Any
// error — dial, deadline, malformed response — means "forecasting degraded",
// surfaced on the Tier-B health panel (doc 14 A13); it never blocks, retries
// into, or otherwise touches the deterministic detection path.
type Client struct {
	cc  *grpc.ClientConn
	svc clockv1.ForecastServiceClient
}

// New connects to a clockd target ("host:port"). The connection is lazy
// (grpc.NewClient); the first RPC drives connectivity, so construction never
// blocks startup even with clockd absent.
func New(target string) (*Client, error) {
	cc, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("clock: dial %s: %w", target, err)
	}
	return &Client{cc: cc, svc: clockv1.NewForecastServiceClient(cc)}, nil
}

// NewFromConn wraps an existing connection (tests use bufconn).
func NewFromConn(cc *grpc.ClientConn) *Client {
	return &Client{cc: cc, svc: clockv1.NewForecastServiceClient(cc)}
}

// Close releases the connection.
func (c *Client) Close() error { return c.cc.Close() }

// Forecast is one clock answer: the point trajectory and the per-level
// quantile trajectories, aligned with the REQUESTED levels.
type Forecast struct {
	Point     []float64
	Quantiles [][]float64 // Quantiles[i][h] — level i (request order), step h
}

// Forecast calls the clock for one bare series. The response is structurally
// validated before use — a malformed wire payload (wrong lengths, echo
// mismatches) is an error, never silently reshaped: a band of the wrong shape
// could otherwise masquerade as a calibrated one.
func (c *Client) Forecast(ctx context.Context, series []float64, horizon int, quantiles []float64) (*Forecast, error) {
	if len(series) == 0 || horizon <= 0 || len(quantiles) == 0 {
		return nil, fmt.Errorf("clock: empty input (series=%d horizon=%d quantiles=%d)", len(series), horizon, len(quantiles))
	}
	resp, err := c.svc.Forecast(ctx, &clockv1.ForecastRequest{
		Series:    series,
		Horizon:   uint32(horizon),
		Quantiles: quantiles,
	})
	if err != nil {
		return nil, fmt.Errorf("clock: forecast: %w", err)
	}
	if int(resp.GetHorizon()) != horizon {
		return nil, fmt.Errorf("clock: response horizon %d != requested %d", resp.GetHorizon(), horizon)
	}
	if int(resp.GetNumQuantiles()) != len(quantiles) {
		return nil, fmt.Errorf("clock: response quantile count %d != requested %d", resp.GetNumQuantiles(), len(quantiles))
	}
	if len(resp.GetPoint()) != horizon {
		return nil, fmt.Errorf("clock: point length %d != horizon %d", len(resp.GetPoint()), horizon)
	}
	if len(resp.GetQuantileValues()) != len(quantiles)*horizon {
		return nil, fmt.Errorf("clock: quantile payload %d != %d×%d", len(resp.GetQuantileValues()), len(quantiles), horizon)
	}
	out := &Forecast{Point: resp.GetPoint(), Quantiles: make([][]float64, len(quantiles))}
	for i := range quantiles {
		out.Quantiles[i] = resp.GetQuantileValues()[i*horizon : (i+1)*horizon]
	}
	return out, nil
}

// Health reports the clock's readiness for the Tier-B degradation panel
// (doc 14 A13). The status code is numeric on the wire (never a string); the
// CALLER maps it to operator-facing text on this side of the boundary.
func (c *Client) Health(ctx context.Context) (ready bool, statusCode uint32, err error) {
	resp, err := c.svc.Health(ctx, &clockv1.HealthRequest{})
	if err != nil {
		return false, 0, fmt.Errorf("clock: health: %w", err)
	}
	return resp.GetReady(), resp.GetStatusCode(), nil
}
