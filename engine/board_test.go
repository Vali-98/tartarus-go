package engine

import "testing"

func TestFromBoardPreservesVisibleState(t *testing.T) {
	var b BoardInput
	for n := 1; n < CellCount; n++ {
		b.Cells[n] = CellInput{Kind: "sealed"}
	}
	b.Cells[1] = CellInput{Kind: "open", Beast: "Lion", Tier: 2, Ivory: true}
	b.Cells[2] = CellInput{Kind: "open", Beast: "Wolf", Tier: 2}
	b.Cells[3] = CellInput{Kind: "sealed", Known: 1, Tier: 3, Scratch: true, Event: "unknown"}
	b.Warden, b.Day, b.Phase, b.Dest = 1, 7, "manage", []int{2}
	b.Tools = []string{"saw"}
	b.NoSwap, b.NoDevour = true, false

	st, message := FromBoard(b)
	if st == nil {
		t.Fatal(message)
	}
	if st.Position != 1 || st.Day != 7 || st.Phase != PhaseManage || !st.Has(ToolSaw) {
		t.Fatalf("board state lost: position=%d day=%d phase=%d tools=%v", st.Position, st.Day, st.Phase, st.Tools)
	}
	if !st.Cells[1].Ivory || !st.Cells[3].Scratch || st.Cells[3].Known != 1 || !st.Cells[3].EventUnknown {
		t.Fatalf("cell details lost: warden=%+v door=%+v", st.Cells[1], st.Cells[3])
	}
	if !st.Used.NoSwap || st.Used.NoDevour || !st.CanDevourNow() || st.CanSwapNow() {
		t.Fatalf("action availability lost: %+v", st.Used)
	}
}

func TestFromBoardAllowsConsumeAfterReplace(t *testing.T) {
	var b BoardInput
	for n := 1; n < CellCount; n++ {
		b.Cells[n] = CellInput{Kind: "sealed"}
	}
	b.Cells[1] = CellInput{Kind: "open", Beast: "Lion", Tier: 1}
	b.Warden, b.Phase, b.UsedSwap, b.NoSwap, b.DevourAfterSwap = 1, "manage", true, true, true
	st, message := FromBoard(b)
	if st == nil {
		t.Fatal(message)
	}
	if !st.CanDevourNow() || st.CanSwapNow() {
		t.Fatalf("after-replace action cards were not preserved: %+v", st.Used)
	}
}

func TestConsumeSelectionTargetsResumeChain(t *testing.T) {
	var b BoardInput
	for n := 1; n < CellCount; n++ {
		b.Cells[n] = CellInput{Kind: "sealed"}
	}
	b.Cells[1] = CellInput{Kind: "open", Beast: "lion", Tier: 0}
	b.Cells[2] = CellInput{Kind: "open", Beast: "wolf", Tier: 0}
	b.Cells[3] = CellInput{Kind: "open", Beast: "wolf", Tier: 1}
	b.Warden, b.Phase, b.StartEvent = 1, "manage", "cagefight"
	b.ChainSelect, b.ChainTargets = true, []int{2}

	st, message := FromBoard(b)
	if st == nil {
		t.Fatal(message)
	}
	options := st.ChainOptions()
	if len(options) != 1 || options[0].Cell != 2 {
		t.Fatalf("current screen offered %v, want only cell 3", options)
	}
	if result := st.ChainBite(2); !result.OK {
		t.Fatal(result.Reason)
	}
	if !st.Chaining.Active || st.Chaining.OffersSet {
		t.Fatalf("follow-on chain state not resumed: %+v", st.Chaining)
	}
	options = st.ChainOptions()
	if len(options) != 1 || options[0].Cell != 3 {
		t.Fatalf("follow-on options %v, want cell 4", options)
	}
}

func TestFreshInstrumentOfferBuildsDayOneState(t *testing.T) {
	var b BoardInput
	for n := 1; n < CellCount; n++ {
		b.Cells[n] = CellInput{Kind: "sealed"}
	}
	b.Day, b.MaxDays, b.Phase, b.FreshOffer = 1, 20, "choice", true
	b.Pending, b.Offer, b.Rerolls = "tool", []string{"crown", "rope", "saw"}, 1
	st, message := FromBoard(b)
	if st == nil {
		t.Fatal(message)
	}
	if st.Phase != PhasePatrol || st.Day != 1 || st.DestN == 0 || len(st.Pending) != 1 {
		t.Fatalf("invalid fresh-offer simulation state: phase=%d day=%d destinations=%d pending=%d", st.Phase, st.Day, st.DestN, len(st.Pending))
	}
	if len(st.Pending[0].Options) != 3 || !st.CanReroll() {
		t.Fatalf("instrument offer was not preserved: %+v", st.Pending)
	}
}

func TestMidrunTreasureOfferPreservesBoardAndPicks(t *testing.T) {
	var b BoardInput
	for n := 1; n < CellCount; n++ {
		b.Cells[n] = CellInput{Kind: "sealed"}
	}
	b.Cells[1] = CellInput{Kind: "open", Beast: "lion", Tier: 3}
	b.Warden, b.Day, b.MaxDays, b.Phase = 1, 6, 20, "choice"
	b.Pending, b.Offer, b.GiftPicks = "gift", []string{"ring", "horn", "wine", "brand"}, 3
	st, message := FromBoard(b)
	if st == nil {
		t.Fatal(message)
	}
	if st.Phase != PhaseManage || st.Day != 6 || len(st.Pending) != 3 || st.Cells[1].Inmate.Tier != 3 {
		t.Fatalf("midrun gift offer state lost: phase=%d day=%d pending=%d inmate=%+v", st.Phase, st.Day, len(st.Pending), st.Cells[1].Inmate)
	}
	for i, choice := range st.Pending {
		if !choice.Gift || len(choice.Options) != int(GiftCount) {
			t.Fatalf("gift pick %d not initialized: %+v", i, choice)
		}
	}
}
