package solver

import (
	"context"
	"testing"

	"tartarus/engine"
)

func TestSolveTypedBoard(t *testing.T) {
	var b engine.BoardInput
	for n := 1; n < 16; n++ {
		b.Cells[n] = engine.CellInput{Kind: "sealed"}
	}
	b.Cells[1] = engine.CellInput{Kind: "open", Beast: "lion", Tier: 2}
	b.Cells[2] = engine.CellInput{Kind: "open", Beast: "wolf", Tier: 2}
	b.Cells[3] = engine.CellInput{Kind: "sealed", Known: 1, Tier: 3}
	b.Cells[4] = engine.CellInput{Kind: "sealed", Event: "execution"}
	b.Warden, b.Day, b.Phase, b.Tools, b.Dest = 0, 4, "patrol", []string{"saw"}, []int{1, 3, 4}
	st, msg := engine.FromBoard(b)
	if st == nil {
		t.Fatal(msg)
	}
	_, label, why, _ := Instant(st)
	t.Logf("instant: %s -- %s", label, why)
	sum := Solve(context.Background(), st, Options{Rollouts: 1500, StopAt: 4})
	t.Logf("%d rounds, done=%v", sum.Rounds, sum.Done)
	for _, o := range sum.Options {
		t.Logf("  %-60s %.3f +-%.3f gap=%.3f", o.Label, o.Mean, o.SE, o.GapSE)
	}
}
