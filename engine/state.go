package engine

import "fmt"

type Inmate struct {
	ID    int32
	Beast Beast
	Tier  int8
}

type Cell struct {
	N            int8
	Sealed       bool
	HasInmate    bool
	Inmate       Inmate
	HasHidden    bool
	Hidden       Inmate
	Known        int8 // 1: the rarity behind the door shows, 2: beast too
	AtLeast      int8
	Scratch      bool
	Nest         bool
	NestDue      int16
	Event        Event
	EventUnknown bool // an event is there but its kind isn't known
	Seen         bool
	Ivory        bool
}

type Choice struct {
	Gift    bool
	Options []uint8 // Tool or Gift ids
	Rerolls int
}

type Prey struct {
	Lion, Prey int32
	PreyBeast  Beast
	Rounds     int
	Next       int
}

type Used struct {
	Swap, Devour    int
	Hunt            bool
	NoDevour        bool
	NoSwap          bool
	DevourAfterSwap bool
}

type Stats struct{ Patrols, Opened, Devours, Chains, Swaps, Executions, Best int }

type Chaining struct {
	Active    bool
	Eater     int32
	Bites     int
	OffersSet bool
	Offers    [CellCount]bool
}

type EventState struct {
	Active bool
	Kind   Event
	Cell   int
}

type LogEntry struct {
	Day  int    `json:"d"`
	Kind string `json:"k"`
	Text string `json:"t"`
}

type State struct {
	Rng        uint32
	Params     Params
	Day        int
	MaxDays    int
	StartEvent StartEvent
	Phase      Phase
	Position   int
	Dest       [CellCount]int8
	DestN      int
	DestBase   int
	Chain      bool
	Ratway     bool
	Event      EventState
	Cells      [CellCount]Cell
	VaultN     int
	Cache      int
	Tools      uint64
	Pending    []Choice
	Objective  Objective
	Progress   int
	Milestone  bool
	Used       Used
	Chaining   Chaining
	Pool       [16]Event
	PoolN      int
	Prey       []Prey
	NextID     int32
	Stats      Stats

	LogOn bool
	Log   []LogEntry
}

// Clone copies a state for a playout. The log is not carried over.
func (s *State) Clone() *State {
	c := *s
	c.Log = nil
	c.LogOn = false
	if len(s.Pending) > 0 {
		c.Pending = append([]Choice(nil), s.Pending...)
	}
	if len(s.Prey) > 0 {
		c.Prey = append([]Prey(nil), s.Prey...)
	}
	return &c
}

func (s *State) Has(t Tool) bool { return s.Tools&(1<<t) != 0 }
func (s *State) AddTool(t Tool)  { s.Tools |= 1 << t }

type Result struct {
	OK     bool
	Reason string
}

var okResult = Result{OK: true}

func fail(reason string) Result { return Result{Reason: reason} }

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func CellLabel(n int) string { return fmt.Sprintf("%02d", n+1) }

func Describe(i Inmate) string {
	return TierNames[clamp(int(i.Tier), 0, Rainbow)] + " " + BeastNames[i.Beast]
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func StepsBetween(a, b int) int { return (b - a + CellCount) % CellCount }

// ---------- Randomness (mulberry32, as the simulator's engine) ----------

func (s *State) rand() float64 {
	s.Rng += 0x6d2b79f5
	t := s.Rng
	t = (t ^ t>>15) * (t | 1)
	t ^= t + (t^t>>7)*(t|61)
	return float64(t^t>>14) / 4294967296
}

func (s *State) pickIndex(w []float64) int {
	total := 0.0
	for _, x := range w {
		if x > 0 {
			total += x
		}
	}
	if total <= 0 {
		return 0
	}
	r := s.rand() * total
	for i, x := range w {
		if x > 0 {
			r -= x
		}
		if r < 0 {
			return i
		}
	}
	return len(w) - 1
}

func (s *State) pickInt(list []int) int { return list[int(s.rand()*float64(len(list)))] }

func (s *State) shuffle(list []int) []int {
	out := append([]int(nil), list...)
	for i := len(out) - 1; i > 0; i-- {
		j := int(s.rand() * float64(i+1))
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// ---------- Logging ----------

func (s *State) logf(kind, format string, args ...any) {
	if !s.LogOn {
		return
	}
	s.Log = append(s.Log, LogEntry{Day: s.Day, Kind: kind, Text: fmt.Sprintf(format, args...)})
	if len(s.Log) > logLimit {
		s.Log = s.Log[len(s.Log)-logLimit:]
	}
}

// ---------- Cell and inmate helpers ----------

func blankCell(n int) Cell { return Cell{N: int8(n), Sealed: n != Office} }

func (s *State) makeInmate(b Beast, tier int) Inmate {
	id := s.NextID
	s.NextID++
	return Inmate{ID: id, Beast: b, Tier: int8(clamp(tier, 0, Rainbow))}
}

func (s *State) doorWeights() [5]float64 {
	w := s.Params.TierWeights
	if s.Has(ToolHook) {
		for _, x := range w[1:] {
			if x > 0 {
				w[0] = 0
				break
			}
		}
	}
	return w
}

func (s *State) rollInmate(w []float64) Inmate {
	tier := s.pickIndex(w)
	beast := Beast(s.pickIndex(s.Params.BeastWeights[:]))
	return s.makeInmate(beast, tier)
}

func (s *State) cellWeights(c *Cell) [5]float64 {
	w := s.doorWeights()
	kept := w
	any := false
	for t := range kept {
		if t < int(c.AtLeast) {
			kept[t] = 0
		}
		if kept[t] > 0 {
			any = true
		}
	}
	if any {
		return kept
	}
	return w
}

func (s *State) hiddenOf(c *Cell) *Inmate {
	if !c.HasHidden {
		w := s.cellWeights(c)
		c.Hidden = s.rollInmate(w[:])
		c.HasHidden = true
	}
	return &c.Hidden
}

func (s *State) doorInmate(c *Cell) Inmate {
	if c.HasHidden && (c.Hidden.Tier > 0 || c.Known > 0 || !s.Has(ToolHook)) {
		return c.Hidden
	}
	w := s.cellWeights(c)
	return s.rollInmate(w[:])
}

// onBoard reports whether cell n is open with an inmate.
func (s *State) onBoard(n int) bool { c := &s.Cells[n]; return !c.Sealed && c.HasInmate }

// boardIdx lists the cells that are open with an inmate.
func (s *State) boardIdx() []int {
	out := make([]int, 0, CellCount)
	for n := range s.Cells {
		if s.onBoard(n) {
			out = append(out, n)
		}
	}
	return out
}

func (s *State) BoardCount() int {
	k := 0
	for n := range s.Cells {
		if s.onBoard(n) {
			k++
		}
	}
	return k
}

func (s *State) cellOfID(id int32) int {
	for n := range s.Cells {
		if s.Cells[n].HasInmate && s.Cells[n].Inmate.ID == id {
			return n
		}
	}
	return -1
}

func (s *State) boardCellOfID(id int32) int {
	n := s.cellOfID(id)
	if n >= 0 && s.Cells[n].Sealed {
		return -1
	}
	return n
}

func (s *State) linkOf(id int32) *Prey {
	for i := range s.Prey {
		if s.Prey[i].Lion == id || s.Prey[i].Prey == id {
			return &s.Prey[i]
		}
	}
	return nil
}

func LinkOf(s *State, id int32) *Prey { return s.linkOf(id) }

func (s *State) sealedIdx(filter func(c *Cell) bool) []int {
	out := make([]int, 0, CellCount)
	for n := range s.Cells {
		if s.Cells[n].Sealed && (filter == nil || filter(&s.Cells[n])) {
			out = append(out, n)
		}
	}
	return out
}

func (s *State) sealedCount() int {
	k := 0
	for n := range s.Cells {
		if s.Cells[n].Sealed {
			k++
		}
	}
	return k
}

// ---------- Diet ----------

func (s *State) CageFight() bool { return s.StartEvent == StartCageFight }

// EatsOf lists the beasts b eats at a tier (tier < 0: the first meal only).
func (s *State) EatsOf(b Beast, tier int) []Beast {
	first := FoodChain[b]
	from := s.Params.DoubleEatTier
	if s.CageFight() {
		from = 0
	}
	if tier >= from && SecondMeal[b] >= 0 {
		return []Beast{first, Beast(SecondMeal[b])}
	}
	return []Beast{first}
}

func (s *State) eats(b Beast, tier int, meal Beast) bool {
	if FoodChain[b] == meal {
		return true
	}
	from := s.Params.DoubleEatTier
	if s.CageFight() {
		from = 0
	}
	return tier >= from && SecondMeal[b] == int8(meal)
}

func (s *State) CanEat(eater, meal Inmate) bool {
	if eater.ID == meal.ID || eater.Tier >= Rainbow {
		return false
	}
	counters := s.eats(eater.Beast, int(eater.Tier), meal.Beast) || (eater.Beast == Rat && meal.Beast == Rat && s.Has(ToolSkewer))
	if eater.Tier != meal.Tier {
		if meal.Tier < eater.Tier && counters && s.CageFight() {
			return true
		}
		if meal.Tier > eater.Tier && counters && eater.Beast == Mammoth && s.Has(ToolMaul) {
			return true
		}
		return eater.Tier > meal.Tier && eater.Beast == Wolf && s.Has(ToolGuillotine) && s.eats(Wolf, -1, meal.Beast)
	}
	return counters || (eater.Beast == Lion && s.Has(ToolCrown))
}

func (s *State) CanDevourNow() bool {
	return s.Used.Devour < 1 && !s.Used.Hunt && !s.Used.NoDevour && (s.Used.Swap < 1 || s.Used.DevourAfterSwap)
}

func (s *State) CanSwapNow() bool {
	return s.Used.Devour < 1 && !s.Used.Hunt && !s.Used.NoSwap && s.Used.Swap < s.Params.SwapsPerDay
}

type Option struct {
	Cell    int
	Scratch bool
}

// DevourOptions lists the meals the inmate in cell n can start a consume on.
func (s *State) DevourOptions(n int) []Option {
	cell := &s.Cells[n]
	if cell.Sealed || !cell.HasInmate {
		return nil
	}
	eater := cell.Inmate
	var out []Option
	for i := range s.Cells {
		c := &s.Cells[i]
		if i == n {
			continue
		}
		if !c.Sealed && c.HasInmate && s.CanEat(eater, c.Inmate) {
			out = append(out, Option{Cell: i})
		} else if c.Sealed && c.Scratch && eater.Beast == Wolf && c.HasHidden && c.Hidden.Tier == eater.Tier && eater.Tier < Rainbow {
			out = append(out, Option{Cell: i, Scratch: true})
		}
	}
	return out
}

func (s *State) StartOptions() []Option {
	cell := &s.Cells[s.Position]
	if !s.CanDevourNow() || s.Phase != PhaseManage || cell.Sealed || !cell.HasInmate {
		return nil
	}
	return s.DevourOptions(s.Position)
}

func (s *State) ChainOptions() []Option {
	if !s.Chaining.Active {
		return nil
	}
	n := s.boardCellOfID(s.Chaining.Eater)
	if n < 0 {
		return nil
	}
	opts := s.DevourOptions(n)
	if !s.Chaining.OffersSet {
		return opts
	}
	filtered := opts[:0]
	for _, o := range opts {
		if s.Chaining.Offers[o.Cell] {
			filtered = append(filtered, o)
		}
	}
	return filtered
}

func (s *State) CanStopChain() bool { return s.Params.ChainStop }

func (s *State) HuntTargets() []int {
	here := &s.Cells[s.Position]
	if !s.Has(ToolTrap) || here.Sealed || !here.HasInmate || here.Inmate.Beast != Lion || s.linkOf(here.Inmate.ID) != nil {
		return nil
	}
	var out []int
	for n := range s.Cells {
		if n != s.Position && s.onBoard(n) && s.CanEat(here.Inmate, s.Cells[n].Inmate) && s.linkOf(s.Cells[n].Inmate.ID) == nil {
			out = append(out, n)
		}
	}
	return out
}

func (s *State) CanHuntNow() bool {
	return s.Phase == PhaseManage && !s.Chaining.Active && s.Used.Devour < 1 && s.Used.Swap < 1 && !s.Used.Hunt && len(s.HuntTargets()) > 0
}

func (s *State) PatrolOptions() []int {
	if s.Phase != PhasePatrol {
		return nil
	}
	out := make([]int, s.DestN)
	for i := 0; i < s.DestN; i++ {
		out[i] = int(s.Dest[i])
	}
	return out
}

func (s *State) ClockwisePath(to int) []int {
	var path []int
	n := s.Position
	for n != to {
		n = (n + 1) % CellCount
		path = append(path, n)
	}
	if len(path) == 0 {
		return []int{to}
	}
	return path
}

func (s *State) CanReroll() bool {
	return len(s.Pending) > 0 && !s.Pending[0].Gift && s.Pending[0].Rerolls > 0
}

func (s *State) InstigateCells() []int {
	var out []int
	for n := 1; n < CellCount; n++ {
		c := &s.Cells[n]
		if c.Event != EvNone {
			continue
		}
		if c.Sealed && !c.Nest || !c.Sealed && c.HasInmate {
			out = append(out, n)
		}
	}
	return out
}

func (s *State) InstigateCount() int {
	for n := range s.Cells {
		if s.Cells[n].Event != EvNone {
			return InstigateEvents
		}
	}
	return InstigateEmpty
}
