package solver

import (
	"context"
	"testing"
	"time"

	"tartarus/engine"
)

func runs(n int) (avg float64, took time.Duration) {
	start := time.Now()
	total := 0
	for i := 0; i < n; i++ {
		st := engine.NewGame(uint32(i+1), engine.NewOpts{StartTools: 1})
		Playout(st, nil, &rng32{s: uint32(i)*31 + 7})
		total += st.VaultN
	}
	return float64(total) / float64(n), time.Since(start)
}

func TestPlayoutAverage(t *testing.T) {
	avg, took := runs(3000)
	t.Logf("avg rainbows %.3f over 3000 runs in %v (%.0f runs/s)", avg, took, 3000/took.Seconds())
	if avg < 1 {
		t.Fatalf("strategy vaults almost nothing: %.3f", avg)
	}
}

func TestSolveBoard(t *testing.T) {
	st := engine.NewGame(42, engine.NewOpts{StartTools: 1})
	for st.Phase != engine.PhaseManage && st.Phase != engine.PhaseEnded {
		a, _ := decide(st, paramsFor(st), &rng32{s: 3})
		Apply(st, a)
	}
	start := time.Now()
	sum := Solve(context.Background(), st, Options{Rollouts: 400, Seed: 5})
	t.Logf("%d options, %d rounds in %v", len(sum.Options), sum.Rounds, time.Since(start))
	for _, o := range sum.Options {
		t.Logf("  %-50s %.3f ±%.3f policy=%v", o.Label, o.Mean, o.SE, o.Policy)
	}
}

func TestFreshInstrumentOfferHasPickAdvice(t *testing.T) {
	var b engine.BoardInput
	for n := 1; n < engine.CellCount; n++ {
		b.Cells[n] = engine.CellInput{Kind: "sealed"}
	}
	b.Day, b.MaxDays, b.Phase, b.FreshOffer = 1, 20, "choice", true
	b.Pending, b.Offer = "tool", []string{"crown", "rope", "saw"}
	st, message := engine.FromBoard(b)
	if st == nil {
		t.Fatal(message)
	}
	action, label, why, ok := Instant(st)
	if !ok || action.Type != ActChoose || label == "" || why == "" {
		t.Fatalf("fresh offer has no actionable recommendation: action=%+v label=%q why=%q", action, label, why)
	}
	t.Logf("start offer advice: %s (%s)", label, why)
}

func BenchmarkPlayout(b *testing.B) {
	for i := 0; i < b.N; i++ {
		st := engine.NewGame(uint32(i+1), engine.NewOpts{StartTools: 1})
		Playout(st, nil, &rng32{s: uint32(i)})
	}
}
