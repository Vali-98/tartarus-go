package solver

import (
	"fmt"
	"strings"

	"tartarus/engine"
)

// rng32 is mulberry32, the solver's own dice (never the game's).
type rng32 struct{ s uint32 }

func (r *rng32) next32() uint32 {
	r.s += 0x6d2b79f5
	t := r.s
	t = (t ^ t>>15) * (t | 1)
	t ^= t + (t^t>>7)*(t|61)
	return t ^ t>>14
}

func (r *rng32) float() float64 { return float64(r.next32()) / 4294967296 }

type ActType uint8

const (
	ActChoose ActType = iota
	ActReroll
	ActEvent
	ActPatrol
	ActDevour
	ActChain
	ActStopChain
	ActOffice
	ActSwap
	ActHunt
	ActEnd
)

// Action is one move. Cell is the target for patrol, devour, chain and hunt.
type Action struct {
	Type   ActType `json:"type"`
	Cell   int     `json:"cell"`
	ID     uint8   `json:"id"`
	Accept bool    `json:"accept"`
	Choice string  `json:"choice"`
}

// decide is the strategy's move from this state (ok false when the run ended).
func decide(st *engine.State, P *Weights, rnd *rng32) (Action, bool) {
	if st.Phase == engine.PhaseEnded {
		return Action{}, false
	}
	c := newContext(st, P)
	if len(st.Pending) > 0 {
		ch := st.Pending[0]
		if ch.Gift {
			return Action{Type: ActChoose, ID: pickGift(st, &c, ch.Options, rnd)}, true
		}
		if wantsReroll(st, P, toolsOf(ch.Options)) {
			return Action{Type: ActReroll}, true
		}
		return Action{Type: ActChoose, ID: uint8(pickTool(st, P, toolsOf(ch.Options)))}, true
	}
	if st.Chaining.Active {
		opts := devourScores(st, &c)
		if c.chainStop && (len(opts) == 0 || opts[0].score < P.StopMargin) {
			return Action{Type: ActStopChain}, true
		}
		if len(opts) > 0 {
			return Action{Type: ActChain, Cell: opts[0].cell}, true
		}
		return Action{Type: ActEnd}, true
	}
	if st.Phase == engine.PhaseOffice {
		if P.Office != "" {
			return Action{Type: ActOffice, Choice: P.Office}, true
		}
		comp, _ := composition(st, -1)
		o := officeValues(st, &c, &comp)
		if o.bestow >= o.instigate {
			return Action{Type: ActOffice, Choice: "bestow"}, true
		}
		return Action{Type: ActOffice, Choice: "instigate"}, true
	}
	if st.Phase == engine.PhaseEvent && st.Event.Active {
		switch k := st.Event.Kind; k {
		case engine.EvChain:
			return Action{Type: ActEvent, Accept: P.AcceptChain}, true
		case engine.EvAppetite, engine.EvConvict, engine.EvCrow, engine.EvFreedom, engine.EvUsurp1, engine.EvUsurp2, engine.EvUsurp3, engine.EvUsurp4, engine.EvRatway, engine.EvHunt, engine.EvPortion:
			return Action{Type: ActEvent, Accept: true}, true
		}
		cell := &st.Cells[st.Event.Cell]
		if !cell.HasInmate {
			return Action{Type: ActEvent}, true
		}
		if st.Event.Kind == engine.EvTrickster {
			return Action{Type: ActEvent, Accept: cell.Inmate.Tier < engine.Supreme}, true
		}
		comp, _ := composition(st, st.Event.Cell)
		return Action{Type: ActEvent, Accept: executionValue(&c, int(cell.Inmate.Tier), int(cell.Inmate.Beast), &comp) > P.ExecMargin}, true
	}
	if st.Phase == engine.PhasePatrol {
		if s := patrolScores(st, &c); len(s) > 0 {
			return Action{Type: ActPatrol, Cell: s[0].Cell}, true
		}
		return Action{Type: ActEnd}, true
	}
	swapGain := swapGainHere(st, &c)
	huntCell, huntVal, haveHunt := bestHunt(st, &c)
	if st.CanDevourNow() {
		d := devourScores(st, &c)
		if len(d) > 0 && c.consumesLeft > 0 && P.WConsume != 0 {
			d[0].score += P.WConsume / float64(c.consumesLeft)
		}
		if len(d) > 0 && d[0].score >= P.MinDevour {
			if haveHunt && huntVal > d[0].score && huntVal > swapGain {
				return Action{Type: ActHunt, Cell: huntCell}, true
			}
			if swapGain > 0 && swapGain > d[0].score+P.SwapMargin {
				return Action{Type: ActSwap}, true
			}
			return Action{Type: ActDevour, Cell: d[0].cell}, true
		}
	}
	if haveHunt && huntVal > 0 && huntVal > swapGain {
		return Action{Type: ActHunt, Cell: huntCell}, true
	}
	if swapGain > 0 {
		return Action{Type: ActSwap}, true
	}
	return Action{Type: ActEnd}, true
}

func wantsReroll(st *engine.State, P *Weights, options []engine.Tool) bool {
	if !st.CanReroll() || !P.ToolSets {
		return false
	}
	now, next, ok := rerollOdds(st, options)
	return ok && next > now+P.RerollMargin
}

// Apply plays an action on a state.
func Apply(st *engine.State, a Action) engine.Result {
	switch a.Type {
	case ActChoose:
		return st.Choose(a.ID)
	case ActReroll:
		return st.RerollOffer()
	case ActEvent:
		return st.ResolveEvent(a.Accept)
	case ActPatrol:
		return st.Patrol(a.Cell)
	case ActDevour:
		return st.Devour(st.Position, a.Cell)
	case ActChain:
		return st.ChainBite(a.Cell)
	case ActStopChain:
		return st.StopChain()
	case ActOffice:
		return st.OfficeAct(a.Choice)
	case ActSwap:
		return st.Swap()
	case ActHunt:
		return st.Hunt(a.Cell)
	case ActEnd:
		return st.EndDay()
	}
	return engine.Result{Reason: "Unknown move."}
}

// Playout plays a run to its end (or a full vault) with the strategy.
func Playout(st *engine.State, P *Weights, rnd *rng32) {
	if P == nil {
		P = paramsFor(st)
	}
	for guard := 0; st.Phase != engine.PhaseEnded && guard < 2000 && st.VaultN < engine.VaultSlots; guard++ {
		a, ok := decide(st, P, rnd)
		if !ok {
			break
		}
		if !Apply(st, a).OK {
			if a.Type == ActEnd || !Apply(st, Action{Type: ActEnd}).OK {
				break
			}
		}
	}
}

// hideFuture re-rolls everything the player can't see.
func hideFuture(st *engine.State, seed uint32) {
	r := &rng32{s: seed}
	st.Rng = uint32(r.float() * 4294967296)
	for n := range st.Cells {
		c := &st.Cells[n]
		if c.Event != engine.EvNone && c.EventUnknown {
			c.Event = st.Pool[int(r.float()*float64(st.PoolN))]
			c.EventUnknown = false
		}
		if !c.Sealed || !c.HasHidden || c.Known >= 2 {
			continue
		}
		if c.Known == 1 {
			c.Hidden.Beast = engine.Beast(int(r.float() * 4))
		} else {
			c.HasHidden = false
		}
	}
}

// ---------- Candidates ----------

type Candidate struct {
	Action Action
	Label  string
}

func eventLabel(k engine.Event) string {
	name := engine.EventNames[k]
	if k == engine.EvChain || k == engine.EvExecution {
		name = "the " + name
	}
	return name
}

func Candidates(st *engine.State) []Candidate {
	var out []Candidate
	if st.Phase == engine.PhaseEnded {
		return out
	}
	if len(st.Pending) > 0 {
		ch := st.Pending[0]
		for _, id := range ch.Options {
			name := engine.ToolNames[id]
			if ch.Gift {
				name = engine.GiftNames[id]
			}
			out = append(out, Candidate{Action{Type: ActChoose, ID: id}, "Take " + name})
		}
		if st.CanReroll() {
			out = append(out, Candidate{Action{Type: ActReroll}, "Re-roll the offer"})
		}
		return out
	}
	if st.Chaining.Active {
		for _, o := range st.ChainOptions() {
			meal := "the scratched door"
			if !o.Scratch {
				meal = "the " + engine.Describe(st.Cells[o.Cell].Inmate)
			}
			out = append(out, Candidate{Action{Type: ActChain, Cell: o.Cell}, fmt.Sprintf("Chain on: eat %s in %s", meal, engine.CellLabel(o.Cell))})
		}
		if st.CanStopChain() {
			out = append(out, Candidate{Action{Type: ActStopChain}, "Stop the chain (Cancel)"})
		}
		return out
	}
	if st.Phase == engine.PhaseOffice {
		return []Candidate{
			{Action{Type: ActOffice, Choice: "bestow"}, "Bestow: 1 random inmate rises a tier"},
			{Action{Type: ActOffice, Choice: "instigate"}, fmt.Sprintf("Instigate: %s on a random cell", plural(st.InstigateCount(), "special event"))},
		}
	}
	if st.Phase == engine.PhaseEvent && st.Event.Active {
		name := eventLabel(st.Event.Kind)
		if st.Event.Kind == engine.EvHunt {
			return []Candidate{{Action{Type: ActEvent, Accept: true}, "Confirm " + name}}
		}
		return []Candidate{{Action{Type: ActEvent, Accept: true}, "Accept " + name}, {Action{Type: ActEvent, Accept: false}, "Skip " + name}}
	}
	if st.Phase == engine.PhasePatrol {
		for _, n := range st.PatrolOptions() {
			c := &st.Cells[n]
			var what string
			switch {
			case st.Chain:
				opens := 0
				for _, k := range st.ClockwisePath(n) {
					if st.Cells[k].Sealed {
						opens++
					}
				}
				what = fmt.Sprintf("the chain opens %s", plural(opens, "door"))
			case n == engine.Office:
				what = "Instigate or Bestow"
			case c.Sealed && c.Nest:
				what = "the rat nest"
			case c.Sealed && c.Event != engine.EvNone:
				what = "a locked door with an event"
			case c.Sealed:
				what = "a locked door"
			case c.HasInmate:
				what = "the " + engine.Describe(c.Inmate)
			default:
				what = "an empty cell"
			}
			where := "cell " + engine.CellLabel(n)
			if n == engine.Office {
				where = "Interrogation Room"
			}
			out = append(out, Candidate{Action{Type: ActPatrol, Cell: n}, fmt.Sprintf("Go to %s (%s)", where, what)})
		}
		return out
	}
	here := &st.Cells[st.Position]
	for _, o := range st.StartOptions() {
		meal := "the scratched door"
		if !o.Scratch {
			meal = "the " + engine.Describe(st.Cells[o.Cell].Inmate)
		}
		out = append(out, Candidate{Action{Type: ActDevour, Cell: o.Cell}, fmt.Sprintf("Consume %s in %s", meal, engine.CellLabel(o.Cell))})
	}
	if st.CanSwapNow() && !here.Sealed && here.HasInmate {
		out = append(out, Candidate{Action{Type: ActSwap}, fmt.Sprintf("Replace the %s in %s", engine.Describe(here.Inmate), engine.CellLabel(int(here.N)))})
	}
	if st.CanHuntNow() {
		for _, n := range st.HuntTargets() {
			out = append(out, Candidate{Action{Type: ActHunt, Cell: n}, fmt.Sprintf("Hunt: a Prey mark on the %s in %s", engine.Describe(st.Cells[n].Inmate), engine.CellLabel(n))})
		}
	}
	label := "End the day"
	if st.Day >= st.MaxDays {
		label = "Finish the run"
	}
	return append(out, Candidate{Action{Type: ActEnd}, label})
}

// Instant is the strategy's own pick with a short reason; no playouts.
func Instant(st *engine.State) (Action, string, string, bool) {
	P := paramsFor(st)
	a, ok := decide(st, P, &rng32{s: 1})
	if !ok {
		return Action{}, "", "", false
	}
	label := ""
	for _, cand := range Candidates(st) {
		if cand.Action == a {
			label = cand.Label
		}
	}
	return a, label, reason(st, a, P), true
}

func reason(st *engine.State, a Action, P *Weights) string {
	c := newContext(st, P)
	switch a.Type {
	case ActPatrol:
		for _, s := range patrolScores(st, &c) {
			if s.Cell == a.Cell && s.Why != "" {
				return strings.ToUpper(s.Why[:1]) + s.Why[1:] + "."
			}
		}
	case ActDevour, ActChain:
		p, n := ChainOdds(st, st.Position, a.Cell)
		return fmt.Sprintf("The best meal: its chain averages %.1f bites, with %.0f%% to reach Rainbow.", n, 100*p)
	case ActStopChain:
		return "No meal left is worth eating; press Cancel and keep them on the block."
	case ActOffice:
		if a.Choice == "bestow" {
			return "A random inmate rises a tier now, which beats a couple of new event doors."
		}
		return "The block is thin, so new event doors are worth more than raising it."
	case ActSwap:
		return fmt.Sprintf("A %s inmate is worth less than a fresh roll on average.", engine.TierNames[st.Cells[st.Position].Inmate.Tier])
	case ActHunt:
		return "A Prey mark: both inmates rise a tier every morning while they stay on the block."
	case ActEnd:
		return "Nothing left today is worth doing."
	case ActEvent:
		if a.Accept {
			return "It adds more than it costs."
		}
		return "It would cost more than it gains."
	case ActChoose:
		if !st.Pending[0].Gift {
			if g, ok := toolGains(st, toolsOf(st.Pending[0].Options)); ok {
				return fmt.Sprintf("Adds about %+.2f Rainbows per run in simulated runs.", g[engine.Tool(a.ID)])
			}
		}
		return "Worth the most on this board right now."
	case ActReroll:
		return "A new offer is likely to hold a better instrument."
	}
	return ""
}
