package clock

import (
	"context"
	"encoding/json"
	"math"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	clockv1 "github.com/Abishai95141/Vigil-Dilijens/proto/gen/go/vigil/clock/v1"
)

// fakeClock is an in-process ForecastService for hermetic client tests: a
// Go re-statement of the stub clock's honest behaviour (flat point, widening
// band), plus configurable malformation to prove the client REFUSES bad wire
// shapes instead of silently reshaping them.
type fakeClock struct {
	clockv1.UnimplementedForecastServiceServer
	malform string // "" | "point-len" | "quant-len" | "numq" | "horizon"
}

func (f *fakeClock) Forecast(_ context.Context, req *clockv1.ForecastRequest) (*clockv1.ForecastResponse, error) {
	h := int(req.GetHorizon())
	last := req.GetSeries()[len(req.GetSeries())-1]
	point := make([]float64, h)
	for i := range point {
		point[i] = last
	}
	qv := make([]float64, 0, len(req.GetQuantiles())*h)
	for _, q := range req.GetQuantiles() {
		z := q - 0.5 // monotone in q; sign gives lower/upper
		for i := 0; i < h; i++ {
			qv = append(qv, last+z*2.0*math.Sqrt(float64(i+1)))
		}
	}
	resp := &clockv1.ForecastResponse{
		Point:          point,
		QuantileValues: qv,
		NumQuantiles:   uint32(len(req.GetQuantiles())),
		Horizon:        uint32(h),
	}
	switch f.malform {
	case "point-len":
		resp.Point = resp.Point[:h-1]
	case "quant-len":
		resp.QuantileValues = resp.QuantileValues[:len(qv)-1]
	case "numq":
		resp.NumQuantiles++
	case "horizon":
		resp.Horizon++
	}
	return resp, nil
}

func (f *fakeClock) Health(context.Context, *clockv1.HealthRequest) (*clockv1.HealthResponse, error) {
	return &clockv1.HealthResponse{Ready: true, StatusCode: 0}, nil
}

func dialFake(t *testing.T, f *fakeClock) *Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	clockv1.RegisterForecastServiceServer(srv, f)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	cc, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	return NewFromConn(cc)
}

// conformanceDoc mirrors clockd/tests/conformance/cases.json — ONE fixture
// file shared across languages (the swap test's substrate).
type conformanceDoc struct {
	Horizon   int       `json:"horizon"`
	Quantiles []float64 `json:"quantiles"`
	Cases     []struct {
		Name   string          `json:"name"`
		Series []float64       `json:"series"`
		Checks map[string]bool `json:"checks"`
	} `json:"cases"`
}

func loadConformance(t *testing.T) conformanceDoc {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "clockd", "tests", "conformance", "cases.json"))
	if err != nil {
		t.Fatalf("shared conformance fixtures: %v", err)
	}
	var doc conformanceDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) == 0 {
		t.Fatal("conformance fixture has no cases")
	}
	return doc
}

// assertContract is the Go half of the conformance suite: the same contract
// properties the Python side asserts (shapes are client-validated; here we
// add per-step monotonicity, the band-never-collapses rule, and flat honesty).
func assertContract(t *testing.T, name string, fc *Forecast, doc conformanceDoc, series []float64, checks map[string]bool) {
	t.Helper()
	for h := 0; h < doc.Horizon; h++ {
		for i := 0; i < len(doc.Quantiles)-1; i++ {
			if fc.Quantiles[i][h] > fc.Quantiles[i+1][h]+1e-9 {
				t.Fatalf("%s: quantile crossing at step %d", name, h)
			}
		}
		if fc.Quantiles[len(doc.Quantiles)-1][h]-fc.Quantiles[0][h] <= 0 {
			t.Fatalf("%s: band collapsed to a line at step %d (doc 01 §3)", name, h)
		}
	}
	if checks["flat_honesty"] {
		last := series[len(series)-1]
		for h, v := range fc.Point {
			if math.Abs(v-last) > 0.05*math.Abs(last) {
				t.Fatalf("%s: flat input grew a trend at step %d (%v vs %v)", name, h, v, last)
			}
		}
	}
}

func TestForecastConformanceAgainstFake(t *testing.T) {
	c := dialFake(t, &fakeClock{})
	doc := loadConformance(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, cs := range doc.Cases {
		fc, err := c.Forecast(ctx, cs.Series, doc.Horizon, doc.Quantiles)
		if err != nil {
			t.Fatalf("%s: %v", cs.Name, err)
		}
		if len(fc.Point) != doc.Horizon || len(fc.Quantiles) != len(doc.Quantiles) {
			t.Fatalf("%s: reshape wrong", cs.Name)
		}
		assertContract(t, cs.Name, fc, doc, cs.Series, cs.Checks)
	}
}

func TestMalformedResponsesRefused(t *testing.T) {
	doc := loadConformance(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, malform := range []string{"point-len", "quant-len", "numq", "horizon"} {
		c := dialFake(t, &fakeClock{malform: malform})
		if _, err := c.Forecast(ctx, doc.Cases[0].Series, doc.Horizon, doc.Quantiles); err == nil {
			t.Errorf("malformation %q accepted — the client must refuse bad wire shapes", malform)
		}
	}
}

func TestEmptyInputRefusedClientSide(t *testing.T) {
	c := dialFake(t, &fakeClock{})
	ctx := context.Background()
	if _, err := c.Forecast(ctx, nil, 8, []float64{0.5}); err == nil {
		t.Error("empty series accepted")
	}
	if _, err := c.Forecast(ctx, []float64{1}, 0, []float64{0.5}); err == nil {
		t.Error("zero horizon accepted")
	}
	if _, err := c.Forecast(ctx, []float64{1}, 8, nil); err == nil {
		t.Error("no quantiles accepted")
	}
}

func TestHealthPassthrough(t *testing.T) {
	c := dialFake(t, &fakeClock{})
	ready, code, err := c.Health(context.Background())
	if err != nil || !ready || code != 0 {
		t.Fatalf("health: ready=%v code=%d err=%v", ready, code, err)
	}
}

// TestNonGatingWhenAbsent: with NO clockd anywhere, construction succeeds
// (lazy dial) and the first call fails FAST against the caller's deadline —
// the degraded path detection never waits on (doc 09 §2).
func TestNonGatingWhenAbsent(t *testing.T) {
	c, err := New("127.0.0.1:1") // nothing listens
	if err != nil {
		t.Fatalf("construction must not require a live clockd: %v", err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.Forecast(ctx, []float64{1, 2, 3}, 4, []float64{0.5}); err == nil {
		t.Fatal("forecast against nothing must fail")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("degraded call took %v — must fail fast at the deadline", elapsed)
	}
}
