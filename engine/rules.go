package engine

import "sort"

// ---------- Growth and objectives ----------

func (s *State) emptyCell(n int) Inmate {
	c := &s.Cells[n]
	old := c.Inmate
	*c = Cell{N: c.N, Sealed: s.Params.ReusableCells}
	return old
}

func (s *State) rollEvent() Event { return s.Pool[int(s.rand()*float64(s.PoolN))] }

// spawnEvent puts a special event behind a random locked door with chance %.
func (s *State) spawnEvent(chance float64) {
	if !(chance > 0) || s.rand()*100 >= chance {
		return
	}
	free := s.sealedIdx(func(c *Cell) bool { return c.N != Office && c.Event == EvNone && !c.Nest })
	if len(free) == 0 {
		return
	}
	n := s.pickInt(free)
	s.Cells[n].Event = s.rollEvent()
	s.Cells[n].EventUnknown = false
	s.Cells[n].Seen = true
	if s.LogOn {
		s.logf("event", "A special event now waits behind cell %s.", CellLabel(n))
	}
}

func (s *State) meetObjective(why string) {
	s.Milestone = true
	if s.Params.Rewards {
		var pool []int
		for t := 0; t < int(ToolCount); t++ {
			if !s.Has(Tool(t)) {
				pool = append(pool, t)
			}
		}
		pool = s.shuffle(pool)
		if len(pool) > 3 {
			pool = pool[:3]
		}
		if len(pool) > 0 {
			opts := make([]uint8, len(pool))
			for i, t := range pool {
				opts[i] = uint8(t)
			}
			s.Pending = append(s.Pending, Choice{Options: opts})
		}
	}
	if s.LogOn {
		s.logf("tool", "Objective met: %s. Choose a tool.", why)
	}
}

func (s *State) countObjective(id Objective, what string) {
	if s.Objective != id || s.Milestone {
		return
	}
	need := ObjectiveNeed[id]
	if s.Progress < need {
		s.Progress++
	}
	if s.Progress >= need {
		s.meetObjective(what)
	}
}

// afterGrowth applies the vault cap, King of the Caged, and moves a Rainbow
// into the vault. The inmate is the one in cell n.
func (s *State) afterGrowth(n int) {
	c := &s.Cells[n]
	if !c.HasInmate {
		return
	}
	in := &c.Inmate
	if in.Tier >= Rainbow && s.VaultN >= VaultSlots {
		in.Tier = Supreme
	}
	if int(in.Tier) > s.Stats.Best {
		s.Stats.Best = int(in.Tier)
	}
	if s.Objective == ObjKing && !s.Milestone && in.Tier >= Supreme {
		s.meetObjective("a " + Describe(*in))
	}
	if in.Tier < Rainbow {
		return
	}
	beast := in.Beast
	s.emptyCell(n)
	s.VaultN++
	s.Cache++
	if s.LogOn {
		s.logf("rainbow", "Rainbow %s! It left cell %s for the vault.", BeastNames[beast], CellLabel(n))
	}
	if s.Params.Rewards {
		picks := 1
		if s.StartEvent == StartOpulence {
			picks = 3
		}
		for k := 0; k < picks; k++ {
			s.Pending = append(s.Pending, Choice{Gift: true, Options: []uint8{0, 1, 2, 3}})
		}
	}
}

func (s *State) rerollBeast(n int) {
	s.Cells[n].Inmate.Beast = Beast(s.pickIndex(s.Params.BeastWeights[:]))
}

func (s *State) promote(n, by int, reroll bool) {
	c := &s.Cells[n]
	if !c.HasInmate {
		return
	}
	c.Inmate.Tier = int8(clamp(int(c.Inmate.Tier)+by, 0, Rainbow))
	if reroll && c.Inmate.Tier < Rainbow {
		s.rerollBeast(n)
	}
	s.afterGrowth(n)
}

// ref identifies an inmate that may leave its cell before it is used.
type ref struct {
	n  int
	id int32
}

func (s *State) refs(idx []int) []ref {
	out := make([]ref, len(idx))
	for i, n := range idx {
		out[i] = ref{n, s.Cells[n].Inmate.ID}
	}
	return out
}

func (s *State) alive(r ref) bool {
	c := &s.Cells[r.n]
	return !c.Sealed && c.HasInmate && c.Inmate.ID == r.id
}

func (s *State) promoteRef(r ref, by int) {
	if s.alive(r) {
		s.promote(r.n, by, false)
	}
}

func (s *State) boardBelow(tier int) []int {
	var out []int
	for _, n := range s.boardIdx() {
		if int(s.Cells[n].Inmate.Tier) < tier {
			out = append(out, n)
		}
	}
	return out
}

func (s *State) checkPrey() {
	if len(s.Prey) == 0 {
		return
	}
	kept := s.Prey[:0]
	for _, l := range s.Prey {
		a, b := s.boardCellOfID(l.Lion), s.boardCellOfID(l.Prey)
		if a >= 0 && b >= 0 && s.Cells[a].Inmate.Beast == Lion && s.Cells[b].Inmate.Beast == l.PreyBeast {
			kept = append(kept, l)
		}
	}
	s.Prey = kept
}

// openCell opens a sealed cell; a patrol opening returns the event behind the door.
func (s *State) openCell(n int, patrol bool, forced *Inmate) Event {
	cell := &s.Cells[n]
	if !cell.Sealed {
		return EvNone
	}
	event := cell.Event
	var in Inmate
	if forced != nil {
		in = s.makeInmate(forced.Beast, int(forced.Tier))
	} else {
		in = s.doorInmate(cell)
	}
	cell.Sealed = false
	cell.Inmate, cell.HasInmate = in, true
	cell.Hidden, cell.HasHidden = Inmate{}, false
	cell.Known, cell.AtLeast, cell.Scratch, cell.Nest, cell.Event, cell.EventUnknown = 0, 0, false, false, EvNone, false
	s.Stats.Opened++
	if s.LogOn {
		s.logf("patrol", "Opened cell %s: %s.", CellLabel(n), Describe(in))
	}
	s.countObjective(ObjDoors, "10 cells opened")
	s.afterGrowth(n)
	same := func() bool { return cell.HasInmate && !cell.Sealed && cell.Inmate.ID == in.ID }
	if patrol && s.Has(ToolDogtag) && same() && cell.Inmate.Tier < Rainbow && s.rand() < 0.25 {
		s.promote(n, 1, false)
	}
	if patrol && s.Has(ToolBlacklist) && s.sealedCount() <= 5 {
		picks := s.shuffle(s.boardBelow(Rainbow))
		if len(picks) > 2 {
			picks = picks[:2]
		}
		for _, r := range s.refs(picks) {
			s.promoteRef(r, 1)
		}
	}
	if patrol && s.Has(ToolFire) && s.rand() < 0.35 && same() {
		if sealed := s.sealedIdx(nil); len(sealed) > 0 {
			t := s.pickInt(sealed)
			cp := cell.Inmate
			s.openCell(t, false, &cp)
		}
	}
	if patrol && s.Has(ToolKeys) && s.rand() < 0.8 {
		for _, step := range [2]int{-1, 1} {
			s.openCell((n+step+CellCount)%CellCount, false, nil)
		}
	}
	if patrol && s.Has(ToolForged) && s.rand() < 0.5 {
		if sealed := s.sealedIdx(nil); len(sealed) > 0 {
			s.openCell(s.pickInt(sealed), false, nil)
		}
	}
	if patrol {
		return event
	}
	return EvNone
}

// ---------- Guards ----------

type need uint8

const (
	needManage need = iota
	needPatrol
)

func (s *State) guard(n need) string {
	switch {
	case s.Phase == PhaseEnded:
		return "This run is over."
	case len(s.Pending) > 0:
		return "Make your pending choice first."
	case s.Chaining.Active:
		return "Finish the consume chain first: pick its next meal, or stop it."
	case s.Phase == PhaseOffice:
		return "Choose Instigate or Bestow first."
	case s.Phase == PhaseEvent:
		return "Decide on the special event first."
	case n == needManage && s.Phase == PhasePatrol:
		return "Patrol first: walk to one of today's destinations."
	case n == needPatrol && s.Phase != PhasePatrol:
		return "Today's patrol is done."
	}
	return ""
}

// ---------- Day flow ----------

func (s *State) destinationLimit() int {
	base := s.DestBase
	if base == 0 {
		base = s.Params.Destinations
	}
	for _, t := range [3]Tool{ToolForged, ToolWhip, ToolStake} {
		if s.Has(t) {
			base--
		}
	}
	if base < 1 {
		base = 1
	}
	return base
}

func (s *State) rollDestinations() {
	fewer := s.rand()*100 < s.Params.FewerDoorsChance
	s.DestBase = s.Params.Destinations
	if fewer {
		s.DestBase--
	}
	if s.DestBase < 1 {
		s.DestBase = 1
	}
	rng := s.Params.PatrolRange
	if rng > CellCount-1 {
		rng = CellCount - 1
	}
	ahead := make([]int, 0, rng)
	for i := 1; i <= rng; i++ {
		ahead = append(ahead, (s.Position+i)%CellCount)
	}
	picks := s.shuffle(ahead)
	if lim := s.destinationLimit(); len(picks) > lim {
		picks = picks[:lim]
	}
	hasEvent := func(n int) bool { c := &s.Cells[n]; return c.Sealed && c.Event != EvNone && c.Seen }
	if s.Params.EventTwoDoors {
		any := false
		for _, n := range picks {
			any = any || hasEvent(n)
		}
		if any {
			var with, without []int
			for _, n := range picks {
				if hasEvent(n) {
					with = append(with, n)
				} else {
					without = append(without, n)
				}
			}
			picks = append(with, without...)
			if len(picks) > 2 {
				picks = picks[:2]
			}
		}
	}
	sort.SliceStable(picks, func(i, j int) bool {
		return StepsBetween(s.Position, picks[i]) < StepsBetween(s.Position, picks[j])
	})
	s.DestN = len(picks)
	for i, n := range picks {
		s.Dest[i] = int8(n)
	}
}

func (s *State) fitDestinations() {
	if s.Phase != PhasePatrol {
		return
	}
	if lim := s.destinationLimit(); s.DestN > lim {
		s.DestN = lim
	}
}

func (s *State) beginDay() {
	s.Phase = PhasePatrol
	s.Used = Used{}
	s.checkPrey()
	for _, link := range append([]Prey(nil), s.Prey...) {
		if link.Next > s.Day {
			continue
		}
		a, b := s.boardCellOfID(link.Lion), s.boardCellOfID(link.Prey)
		if s.LogOn {
			s.logf("prey", "Prey mark: the Lion and its prey each rise a tier.")
		}
		for i := range s.Prey {
			if s.Prey[i].Lion == link.Lion && s.Prey[i].Prey == link.Prey {
				s.Prey[i].Rounds--
				s.Prey[i].Next = s.Day + 1
				if s.Prey[i].Rounds <= 0 {
					s.Prey = append(s.Prey[:i], s.Prey[i+1:]...)
				}
				break
			}
		}
		if a >= 0 {
			s.promote(a, 1, false)
		}
		if b >= 0 {
			s.promote(b, 1, false)
		}
	}
	s.checkPrey()
	for n := range s.Cells {
		c := &s.Cells[n]
		if c.Nest && int(c.NestDue) <= s.Day {
			if s.LogOn {
				s.logf("nest", "The rat nest in cell %s hatched.", CellLabel(n))
			}
			s.openCell(n, false, &Inmate{Beast: Rat, Tier: int8(s.Params.NestTier)})
		}
	}
	if s.Has(ToolTongs) {
		var mam []int
		for _, n := range s.boardIdx() {
			if s.Cells[n].Inmate.Beast == Mammoth && !s.Cells[n].Ivory {
				mam = append(mam, n)
			}
		}
		if len(mam) > 0 {
			s.Cells[s.pickInt(mam)].Ivory = true
		}
	}
	if s.Has(ToolClaws) {
		if free := s.sealedIdx(func(c *Cell) bool { return !c.Scratch && !c.Nest }); len(free) > 0 {
			n := s.pickInt(free)
			s.hiddenOf(&s.Cells[n])
			s.Cells[n].Scratch = true
			if s.Cells[n].Known < 1 {
				s.Cells[n].Known = 1
			}
		}
	}
	if s.Has(ToolIronbox) {
		if free := s.sealedIdx(func(c *Cell) bool { return !c.Nest }); len(free) > 0 {
			n := s.pickInt(free)
			s.Cells[n].Nest = true
			s.Cells[n].NestDue = int16(s.Day + s.Params.NestDays)
		}
	}
	if s.Has(ToolRavenous) {
		s.ravenousChains()
	}
	if s.Has(ToolCoin) {
		s.coinOfFate()
	}
	if s.Day > 1 {
		s.spawnEvent(s.Params.EventMorningChance)
	}
	s.rollDestinations()
}

func (s *State) coinOfFate() {
	kind := func(n int) int { c := &s.Cells[n].Inmate; return int(c.Beast)*8 + int(c.Tier) }
	board := s.boardIdx()
	var count [32]int
	for _, n := range board {
		count[kind(n)]++
	}
	var matching []int
	for _, n := range board {
		if count[kind(n)] >= 2 {
			matching = append(matching, n)
		}
	}
	if len(matching) == 0 || s.rand() >= 0.6 {
		return
	}
	gone := s.pickInt(matching)
	var same []int
	for _, n := range matching {
		if n != gone && kind(n) == kind(gone) {
			same = append(same, n)
		}
	}
	lucky := s.pickInt(same)
	s.emptyCell(gone)
	s.checkPrey()
	s.promote(lucky, 2, false)
}

func (s *State) ravenousChains() {
	board := s.boardIdx()
	if len(board) == 0 {
		return
	}
	top := int8(-1)
	for _, n := range board {
		if s.Cells[n].Inmate.Tier > top {
			top = s.Cells[n].Inmate.Tier
		}
	}
	var tops []int
	for _, n := range board {
		if s.Cells[n].Inmate.Tier == top {
			tops = append(tops, n)
		}
	}
	n := s.pickInt(tops)
	var sides []int
	for _, k := range [2]int{(n + CellCount - 1) % CellCount, (n + 1) % CellCount} {
		if s.onBoard(k) {
			sides = append(sides, k)
		}
	}
	if len(sides) == 0 {
		return
	}
	rises := 0
	for _, k := range sides {
		s.emptyCell(k)
		if s.rand() < 0.4 {
			rises++
		}
	}
	s.checkPrey()
	if rises > 0 {
		s.promote(n, rises, false)
	}
}

// ---------- Patrol ----------

func (s *State) hasDest(n int) bool {
	for i := 0; i < s.DestN; i++ {
		if int(s.Dest[i]) == n {
			return true
		}
	}
	return false
}

func (s *State) Patrol(n int) Result {
	if r := s.guard(needPatrol); r != "" {
		return fail(r)
	}
	if !s.hasDest(n) {
		return fail("Patrol one of today's destinations.")
	}
	cell := &s.Cells[n]
	ratway := s.Ratway
	s.Ratway = false
	steps := 0
	if !ratway {
		steps = StepsBetween(s.Position, n)
		s.Stats.Patrols++
	}
	event := EvNone
	switch {
	case s.Chain && !ratway:
		for _, k := range s.ClockwisePath(n) {
			s.openCell(k, false, nil)
		}
		s.Chain = false
	case cell.Sealed:
		event = s.openCell(n, true, nil)
	}
	if event == EvNone && !cell.Sealed && cell.HasInmate && cell.Event != EvNone {
		event = cell.Event
		cell.Event = EvNone
	}
	s.Position = n
	s.checkPrey()
	if !ratway {
		s.walked(steps)
	}
	s.spawnEvent(s.Params.EventWalkChance)
	if event != EvNone && s.Cells[n].HasInmate {
		s.Phase = PhaseEvent
		s.Event = EventState{Active: true, Kind: event, Cell: n}
		s.countObjective(ObjIncident, "2 Prison Incidents")
		if s.LogOn {
			s.logf("event", "Cell %s held a special event: %s.", CellLabel(n), EventNames[event])
		}
	} else {
		if n == Office {
			s.Phase = PhaseOffice
		} else {
			s.Phase = PhaseManage
		}
		if n == Office && s.StartEvent == StartTyranny {
			for i := 0; i < 3; i++ {
				s.instigate()
				s.bestow()
			}
		}
	}
	return okResult
}

func (s *State) OfficeAct(choice string) Result {
	if s.Phase != PhaseOffice {
		return fail("Instigate and Bestow come with a visit to the warden's office.")
	}
	if len(s.Pending) > 0 {
		return fail("Make your pending choice first.")
	}
	switch choice {
	case "instigate":
		s.instigate()
	case "bestow":
		s.bestow()
	default:
		return fail("Choose Instigate or Bestow.")
	}
	s.Phase = PhaseManage
	return okResult
}

func (s *State) instigate() {
	cells := s.shuffle(s.InstigateCells())
	if k := s.InstigateCount(); len(cells) > k {
		cells = cells[:k]
	}
	for _, n := range cells {
		s.Cells[n].Event = s.rollEvent()
		s.Cells[n].EventUnknown = false
		s.Cells[n].Seen = true
	}
	if s.LogOn {
		s.logf("event", "Instigate: %d special event(s) placed.", len(cells))
	}
}

func (s *State) bestow() {
	pool := s.boardBelow(Rainbow)
	if len(pool) == 0 {
		return
	}
	n := s.pickInt(pool)
	if s.LogOn {
		s.logf("event", "Bestow: the %s in cell %s rises a tier.", Describe(s.Cells[n].Inmate), CellLabel(n))
	}
	s.promote(n, 1, false)
}

func (s *State) walked(steps int) {
	if s.Has(ToolShortchain) && steps <= 3 && s.rand() < 0.5 {
		if sealed := s.sealedIdx(nil); len(sealed) > 0 {
			s.openCell(s.pickInt(sealed), false, nil)
		}
	}
	if s.Has(ToolShackles) && steps >= 4 && s.rand() < 0.4 {
		if pool := s.boardBelow(Rainbow); len(pool) > 0 {
			s.promote(s.pickInt(pool), 1, false)
		}
	}
	if s.Objective != ObjWalk || s.Milestone {
		return
	}
	s.Progress += steps
	if s.Progress > ObjectiveNeed[ObjWalk] {
		s.Progress = ObjectiveNeed[ObjWalk]
	}
	if s.Progress >= ObjectiveNeed[ObjWalk] {
		s.meetObjective("18 tiles moved")
	}
}

// ---------- Special events ----------

func (s *State) ExecutionVictims(n int) []int {
	if !s.onBoard(n) {
		return nil
	}
	exec := s.Cells[n].Inmate
	food := FoodChain[exec.Beast]
	var out []int
	for _, k := range s.boardIdx() {
		in := s.Cells[k].Inmate
		if k == n || in.Beast != food {
			continue
		}
		if s.Params.ExecutionSameTier && in.Tier == exec.Tier || !s.Params.ExecutionSameTier && in.Tier < exec.Tier {
			out = append(out, k)
		}
	}
	return out
}

func (s *State) scarceBeasts() []Beast {
	var counts [4]int
	for _, n := range s.boardIdx() {
		counts[s.Cells[n].Inmate.Beast]++
	}
	low := counts[0]
	for _, c := range counts {
		if c < low {
			low = c
		}
	}
	var out []Beast
	for b, c := range counts {
		if c == low {
			out = append(out, Beast(b))
		}
	}
	return out
}

func (s *State) lowestInmates() []int {
	board := s.boardIdx()
	low := int8(99)
	for _, n := range board {
		if s.Cells[n].Inmate.Tier < low {
			low = s.Cells[n].Inmate.Tier
		}
	}
	var out []int
	for _, n := range board {
		if s.Cells[n].Inmate.Tier == low {
			out = append(out, n)
		}
	}
	return out
}

func (s *State) ResolveEvent(accept bool) Result {
	r := s.resolveEvent(accept)
	s.acted(r)
	return r
}

func (s *State) acted(r Result) {
	if r.OK && !s.Chaining.Active {
		s.spawnEvent(s.Params.EventActionChance)
	}
}

func (s *State) resolveEvent(accept bool) Result {
	if s.Phase != PhaseEvent || !s.Event.Active {
		return fail("There is no special event to decide.")
	}
	if len(s.Pending) > 0 {
		return fail("Make your pending choice first.")
	}
	kind, n := s.Event.Kind, s.Event.Cell
	finish := func() Result {
		s.Event = EventState{}
		s.Phase = PhaseManage
		return okResult
	}
	if !accept {
		if s.LogOn {
			s.logf("event", "Skipped the %s.", EventNames[kind])
		}
		return finish()
	}
	switch {
	case kind == EvRatway:
		s.Event = EventState{}
		s.Phase = PhasePatrol
		s.Ratway = true
		s.DestN = 0
		for k := 1; k < CellCount; k++ {
			s.Dest[s.DestN] = int8(k)
			s.DestN++
		}
		return okResult
	case kind == EvChain:
		s.Chain = true
	case kind == EvTrickster:
		if !s.onBoard(n) {
			return fail("Nobody is left in that cell for the event to change.")
		}
		scarce := s.scarceBeasts()
		s.Cells[n].Inmate.Beast = scarce[int(s.rand()*float64(len(scarce)))]
		s.Cells[n].Inmate.Tier = Supreme
		s.checkPrey()
		s.afterGrowth(n)
	case kind == EvCrow:
		seen := 0
		for i := 1; i < CellCount && seen < CrowCells; i++ {
			c := &s.Cells[(n+i)%CellCount]
			if !c.Sealed || c.Nest {
				continue
			}
			s.hiddenOf(c)
			if c.Known < 1 {
				c.Known = 1
			}
			seen++
		}
	case kind == EvHunt:
		for k := range s.Cells {
			c := &s.Cells[k]
			if !c.Sealed || c.Nest {
				continue
			}
			if s.hiddenOf(c).Tier < HuntBelow {
				if c.Known < 1 {
					c.Known = 1
				}
			} else if c.AtLeast < HuntBelow {
				c.AtLeast = HuntBelow
			}
		}
	case kind == EvPortion:
		picks := s.shuffle(s.boardBelow(Rainbow))
		if len(picks) > PortionInmates {
			picks = picks[:PortionInmates]
		}
		for _, r := range s.refs(picks) {
			s.promoteRef(r, 1)
		}
	case kind >= EvUsurp1 && kind <= EvUsurp4:
		u := usurpOf[kind]
		pool := s.boardBelow(Rainbow)
		picks := pool
		if u.count >= 0 {
			picks = s.shuffle(pool)
			if len(picks) > u.count {
				picks = picks[:u.count]
			}
		}
		for _, r := range s.refs(picks) {
			s.promoteRef(r, u.by)
		}
		kept := s.Pool[:0]
		for _, e := range s.Pool[:s.PoolN] {
			if e != kind {
				kept = append(kept, e)
			}
		}
		s.PoolN = len(kept)
		if u.next != EvNone {
			found := false
			for _, e := range s.Pool[:s.PoolN] {
				found = found || e == u.next
			}
			if !found {
				s.Pool[s.PoolN] = u.next
				s.PoolN++
			}
		}
		if kind == EvUsurp1 {
			if free := s.sealedIdx(func(c *Cell) bool { return c.Event == EvNone && !c.Nest }); len(free) > 0 {
				d := s.pickInt(free)
				s.Cells[d].Event, s.Cells[d].EventUnknown, s.Cells[d].Seen = EvUsurp2, false, true
			}
		}
	case kind == EvFreedom:
		doors := s.shuffle(s.sealedIdx(nil))
		if len(doors) > FreedomCells {
			doors = doors[:FreedomCells]
		}
		for _, d := range doors {
			s.openCell(d, false, nil)
		}
	case kind == EvConvict:
		if sealed := s.sealedIdx(nil); len(sealed) > 0 {
			t := s.pickInt(sealed)
			var w [5]float64
			for i := 3; i < 5; i++ {
				w[i] = s.Params.TierWeights[i]
			}
			tier := 3
			if w[3] > 0 || w[4] > 0 {
				tier = s.pickIndex(w[:])
			}
			beast := Beast(s.pickIndex(s.Params.BeastWeights[:]))
			s.openCell(t, false, &Inmate{Beast: beast, Tier: int8(tier)})
		}
	case kind == EvAppetite:
		low := s.lowestInmates()
		if len(low) == 0 {
			break
		}
		w := s.pickInt(low)
		gain := 0
		for i := 0; i < AppetiteTries; i++ {
			if s.rand() < AppetiteOdds {
				gain++
			}
		}
		if gain > 0 {
			s.promote(w, gain, false)
		}
	default: // execution
		if !s.onBoard(n) {
			return fail("Nobody is left in that cell to carry out the execution.")
		}
		victims := s.ExecutionVictims(n)
		for _, v := range victims {
			s.emptyCell(v)
		}
		s.Stats.Executions++
		s.checkPrey()
		if len(victims) > 0 {
			s.promote(n, len(victims), s.Params.ExecutionRerolls)
		}
	}
	if s.LogOn {
		s.logf("event", "Accepted the %s.", EventNames[kind])
	}
	return finish()
}

// ---------- Consume ----------

type bite struct{ gain int }

func (s *State) doDevour(from int, opt Option, chained bool) {
	eaterCell := &s.Cells[from]
	mealCell := &s.Cells[opt.Cell]
	eater := eaterCell.Inmate
	meal := mealCell.Inmate
	if mealCell.Sealed {
		meal = mealCell.Hidden
	}
	s.countObjective(ObjConsume, "4 consumes")
	wasRat, wasLion := eater.Beast == Rat, eater.Beast == Lion
	s.emptyCell(opt.Cell)
	crowd := s.BoardCount()
	gain := 1
	if opt.Scratch {
		gain = 2
		if s.Params.ScratchStacksBase {
			gain = 3
		}
	}
	if !opt.Scratch && eater.Beast == Mammoth && s.Has(ToolMaul) {
		gain = int(meal.Tier) + 2 - int(eater.Tier)
		if gain < 1 {
			gain = 1
		}
	}
	if s.Has(ToolMuzzle) && crowd <= 4 {
		gain += 2
	}
	if s.Has(ToolSkewer) && wasRat {
		gain++
	}
	if s.Has(ToolCrown) && wasLion {
		gain += 2
	}
	if s.Has(ToolSaw) && s.rand() < 0.25 {
		gain++
	}
	if s.Has(ToolWhip) && s.rand() < 0.35 {
		gain++
	}
	if chained && s.Has(ToolCollar) && s.rand() < 0.8 {
		gain++
	}
	revenge := s.Has(ToolRevenge) && s.rand() < 0.3
	s.promote(from, gain, !s.CageFight())
	s.Stats.Devours++
	if s.LogOn {
		if s.LogOn {
			s.logf("devour", "The %s in cell %s consumed the %s in cell %s (+%d).", Describe(eater), CellLabel(from), Describe(meal), CellLabel(opt.Cell), gain)
		}
	}
	s.checkPrey()
	id := eater.ID
	if revenge && s.boardCellOfID(id) >= 0 {
		if sealed := s.sealedIdx(nil); len(sealed) > 0 {
			t := s.pickInt(sealed)
			cur := s.Cells[s.boardCellOfID(id)].Inmate
			foods := s.EatsOf(cur.Beast, int(cur.Tier))
			food := foods[int(s.rand()*float64(len(foods)))]
			s.openCell(t, false, &Inmate{Beast: food, Tier: cur.Tier})
		}
	}
	if s.Has(ToolCage) && mealCell.Sealed && s.rand() < 0.4 {
		s.openCell(opt.Cell, false, nil)
	}
}

func (s *State) Devour(from, to int) Result {
	r := s.devour(from, to)
	s.acted(r)
	return r
}

func (s *State) devour(from, to int) Result {
	if r := s.guard(needManage); r != "" {
		return fail(r)
	}
	if s.Used.Swap > 0 && !s.Used.DevourAfterSwap {
		return fail("You replaced an inmate today; a day has a Replace or a consume, not both.")
	}
	if !s.CanDevourNow() {
		return fail("Today's consume is used.")
	}
	if from != s.Position {
		return fail("Only the inmate in the warden's cell can start a consume.")
	}
	var opt *Option
	for _, o := range s.DevourOptions(from) {
		if o.Cell == to {
			o := o
			opt = &o
		}
	}
	if opt == nil {
		return fail("That inmate cannot consume that target.")
	}
	s.Used.Devour++
	s.Chaining = Chaining{Active: true, Eater: s.Cells[from].Inmate.ID, Bites: 1}
	s.doDevour(from, *opt, false)
	s.goOnChain()
	return okResult
}

func (s *State) goOnChain() {
	stoppable := s.CanStopChain()
	for {
		n := s.boardCellOfID(s.Chaining.Eater)
		if n < 0 {
			break
		}
		opts := s.DevourOptions(n)
		if len(opts) == 0 {
			break
		}
		if len(opts) > 1 || stoppable {
			return
		}
		s.doDevour(n, opts[0], true)
		s.Stats.Chains++
		s.Chaining.Bites++
	}
	s.Chaining = Chaining{}
}

func (s *State) ChainBite(to int) Result {
	r := s.chainBite(to)
	s.acted(r)
	return r
}

func (s *State) chainBite(to int) Result {
	if !s.Chaining.Active {
		return fail("No consume chain is waiting for a meal.")
	}
	if len(s.Pending) > 0 {
		return fail("Make your pending choice first.")
	}
	n := s.boardCellOfID(s.Chaining.Eater)
	if n >= 0 {
		for _, o := range s.ChainOptions() {
			if o.Cell == to {
				s.Chaining.OffersSet = false
				s.doDevour(n, o, true)
				s.Stats.Chains++
				s.Chaining.Bites++
				s.goOnChain()
				return okResult
			}
		}
	}
	return fail("The chain can't take that meal.")
}

func (s *State) StopChain() Result {
	if !s.Chaining.Active {
		return fail("No consume chain is waiting for a meal.")
	}
	if !s.CanStopChain() {
		return fail("A consume chain must go on while it has a meal.")
	}
	if len(s.Pending) > 0 {
		return fail("Make your pending choice first.")
	}
	s.Chaining = Chaining{}
	s.acted(okResult)
	return okResult
}

// ---------- Replace ----------

func (s *State) Swap() Result {
	r := s.swap()
	s.acted(r)
	return r
}

func (s *State) swap() Result {
	if r := s.guard(needManage); r != "" {
		return fail(r)
	}
	n := s.Position
	cell := &s.Cells[n]
	if cell.Sealed || !cell.HasInmate {
		return fail("The warden's cell has no inmate to replace.")
	}
	if s.Used.Devour > 0 {
		return fail("You consumed today; a day has a consume or a Replace, not both.")
	}
	if s.Used.Swap >= s.Params.SwapsPerDay {
		return fail("No Replaces left today.")
	}
	if s.Used.NoSwap {
		return fail("Your game offers no Replace today.")
	}
	w := s.Params.SwapWeights
	if s.Has(ToolRope) {
		w[0], w[1] = 0, 0
	}
	if w[0] <= 0 && w[1] <= 0 && w[2] <= 0 && w[3] <= 0 && w[4] <= 0 {
		return fail("No rarity is left to roll.")
	}
	old := cell.Inmate
	cell.HasInmate = false
	s.checkPrey()
	above := false
	if s.Has(ToolScapegoat) {
		others := s.boardIdx()
		if len(others) >= 2 {
			for _, v := range s.shuffle(others)[:2] {
				s.emptyCell(v)
			}
			s.checkPrey()
			above = true
		}
	}
	var higher [5]float64
	anyHigher := false
	for t := range w {
		if t > int(old.Tier) {
			higher[t] = w[t]
			anyHigher = anyHigher || w[t] > 0
		}
	}
	var fresh Inmate
	if above && anyHigher {
		fresh = s.rollInmate(higher[:])
	} else {
		fresh = s.rollInmate(w[:])
	}
	if above && fresh.Tier <= old.Tier {
		fresh.Tier = int8(clamp(int(old.Tier)+1, 0, Rainbow))
	}
	if s.Has(ToolStake) && old.Tier < 3 && s.rand() < 0.4 && fresh.Tier != 3 {
		fresh.Tier = 3
	}
	if s.Has(ToolPestle) {
		fresh.Beast = old.Beast
	}
	cell.Inmate, cell.HasInmate = fresh, true
	s.Used.Swap++
	s.Stats.Swaps++
	if s.LogOn {
		s.logf("swap", "Replaced the %s in cell %s. In came a %s.", Describe(old), CellLabel(n), Describe(fresh))
	}
	if s.Has(ToolMaiden) {
		aboveN, belowN := 0, 0
		for _, k := range s.boardIdx() {
			if s.Cells[k].Inmate.ID == fresh.ID {
				continue
			}
			t := s.Cells[k].Inmate.Tier
			if t > fresh.Tier {
				aboveN++
			} else if t < fresh.Tier {
				belowN++
			}
		}
		if aboveN == belowN {
			s.promote(n, 3, false)
		}
	}
	s.afterGrowth(n)
	if s.Has(ToolAxe) && fresh.Tier > old.Tier {
		doors := s.shuffle(s.sealedIdx(nil))
		if len(doors) > 2 {
			doors = doors[:2]
		}
		for _, d := range doors {
			s.openCell(d, false, nil)
		}
	}
	if s.Has(ToolScales) && s.rand() < 0.8 {
		s.scout()
	}
	if cell.Ivory && s.onBoard(n) && cell.Inmate.ID == fresh.ID {
		pool := make([]int, 0, CellCount)
		for _, k := range s.boardIdx() {
			if s.Params.IvoryIncludesSwapped || s.Cells[k].Inmate.ID != fresh.ID {
				pool = append(pool, k)
			}
		}
		picks := s.shuffle(pool)
		if len(picks) > s.Params.IvoryCount {
			picks = picks[:s.Params.IvoryCount]
		}
		for _, r := range s.refs(picks) {
			s.promoteRef(r, 1)
		}
	}
	return okResult
}

func (s *State) scout() {
	doors := s.sealedIdx(func(c *Cell) bool { return !c.Nest })
	if len(doors) == 0 {
		return
	}
	top := int8(-1)
	for _, n := range doors {
		if t := s.hiddenOf(&s.Cells[n]).Tier; t > top {
			top = t
		}
	}
	var tops []int
	for _, n := range doors {
		if s.Cells[n].Hidden.Tier == top {
			tops = append(tops, n)
		}
	}
	s.Cells[s.pickInt(tops)].Known = 2
}

// ---------- Hunt ----------

func (s *State) Hunt(to int) Result {
	r := s.hunt(to)
	s.acted(r)
	return r
}

func (s *State) hunt(to int) Result {
	if r := s.guard(needManage); r != "" {
		return fail(r)
	}
	if !s.Has(ToolTrap) {
		return fail("Only the Blood-Stained Trap offers a Hunt.")
	}
	if s.Used.Devour > 0 || s.Used.Swap > 0 || s.Used.Hunt {
		return fail("A day has a Consume, a Replace or a Hunt.")
	}
	ok := false
	for _, t := range s.HuntTargets() {
		ok = ok || t == to
	}
	if !ok {
		return fail("A Hunt marks an inmate the Lion in the warden's cell can consume.")
	}
	lion, prey := s.Cells[s.Position].Inmate, s.Cells[to].Inmate
	s.Used.Hunt = true
	s.Prey = append(s.Prey, Prey{Lion: lion.ID, Prey: prey.ID, PreyBeast: prey.Beast, Rounds: s.Params.PreyRounds, Next: s.Day + 1})
	return okResult
}

// ---------- Day end ----------

func (s *State) EndDay() Result {
	if r := s.guard(needManage); r != "" {
		return fail(r)
	}
	if s.Day >= s.MaxDays {
		s.finish()
		return okResult
	}
	s.Day++
	if s.LogOn {
		s.logf("day", "Day %d of %d.", s.Day, s.MaxDays)
	}
	s.beginDay()
	return okResult
}

func (s *State) finish() {
	s.Phase = PhaseEnded
	s.DestN = 0
	s.Event = EventState{}
	s.Chain = false
}

// ---------- Choices ----------

func (s *State) Choose(id uint8) Result {
	if len(s.Pending) == 0 {
		return fail("There is nothing to choose right now.")
	}
	choice := s.Pending[0]
	found := false
	for _, o := range choice.Options {
		found = found || o == id
	}
	if !found {
		return fail("That option is not on offer.")
	}
	s.Pending = s.Pending[1:]
	if choice.Gift {
		s.applyGift(Gift(id))
	} else {
		s.AddTool(Tool(id))
		if s.LogOn {
			s.logf("tool", "Took the %s.", ToolNames[id])
		}
		if s.Phase == PhasePatrol {
			s.fitDestinations()
		}
	}
	if s.Phase == PhaseEvent && s.Event.Active && !s.Cells[s.Event.Cell].HasInmate {
		s.Event = EventState{}
		s.Phase = PhaseManage
	}
	return okResult
}

func (s *State) RerollOffer() Result {
	if !s.CanReroll() {
		return fail("There is no re-roll left for this offer.")
	}
	ch := &s.Pending[0]
	var pool []int
	for t := 0; t < int(ToolCount); t++ {
		if s.Has(Tool(t)) {
			continue
		}
		onOffer := false
		for _, o := range ch.Options {
			onOffer = onOffer || int(o) == t
		}
		if !onOffer {
			pool = append(pool, t)
		}
	}
	if len(pool) < 3 {
		pool = pool[:0]
		for t := 0; t < int(ToolCount); t++ {
			if !s.Has(Tool(t)) {
				pool = append(pool, t)
			}
		}
	}
	pool = s.shuffle(pool)
	if len(pool) > 3 {
		pool = pool[:3]
	}
	opts := make([]uint8, len(pool))
	for i, t := range pool {
		opts[i] = uint8(t)
	}
	ch.Options = opts
	ch.Rerolls--
	return okResult
}

func (s *State) applyGift(g Gift) {
	switch g {
	case GiftRing:
		doors := s.shuffle(s.sealedIdx(nil))
		if len(doors) > 3 {
			doors = doors[:3]
		}
		for _, d := range doors {
			s.openCell(d, false, nil)
		}
	case GiftHorn:
		var ids []int32
		for _, n := range s.boardIdx() {
			if len(s.DevourOptions(n)) > 0 {
				ids = append(ids, s.Cells[n].Inmate.ID)
			}
		}
		for _, id := range ids {
			n := s.boardCellOfID(id)
			if n < 0 {
				continue
			}
			var opts []Option
			for _, o := range s.DevourOptions(n) {
				if !o.Scratch {
					opts = append(opts, o)
				}
			}
			if len(opts) == 0 {
				continue
			}
			s.doDevour(n, opts[int(s.rand()*float64(len(opts)))], false)
		}
	case GiftWine:
		for _, n := range s.boardBelow(3) {
			s.promote(n, 1, false)
		}
	case GiftBrand:
		var hungry []int
		for _, n := range s.boardIdx() {
			if len(s.DevourOptions(n)) == 0 {
				hungry = append(hungry, n)
			}
		}
		hungry = s.shuffle(hungry)
		if len(hungry) > 4 {
			hungry = hungry[:4]
		}
		for _, n := range hungry {
			if !s.onBoard(n) || len(s.DevourOptions(n)) > 0 {
				continue
			}
			var possible []int
			for b := Beast(0); b < 4; b++ {
				probe := Inmate{ID: s.Cells[n].Inmate.ID, Beast: b, Tier: s.Cells[n].Inmate.Tier}
				for _, k := range s.boardIdx() {
					if s.CanEat(probe, s.Cells[k].Inmate) {
						possible = append(possible, int(b))
						break
					}
				}
			}
			if len(possible) > 0 {
				s.Cells[n].Inmate.Beast = Beast(s.pickInt(possible))
			}
		}
		s.checkPrey()
	}
}
