// Package solver is the Monte Carlo move advisor, ported from solver.js: chain
// math, a rules-based strategy, and a parallel solver that plays every
// candidate move out many times with everything hidden re-rolled.
package solver

import (
	"math"

	"tartarus/engine"
)

const (
	rainbow = engine.Rainbow
	inf     = math.MaxFloat64
)

// Weights are the strategy's tuned values (see the simulator's tools/README).
type Weights struct {
	TierBase, RainbowMult                           float64
	WOpen, WNow, WWalk, WStep, WNest, WChain, WGoal float64
	WIncident, WPass, WOfficeIncident, WConsume     float64
	WConsumeWalk, WInstigate, WCrow, WShort, WKeys  float64
	WForged, WFire, WCage, RerollMargin, WStair     float64
	MinDevour, StopMargin, SwapMargin, ExecMargin   float64
	SwapMaxTier, MaidenSwapTier                     int
	AcceptChain, ToolSets, GiftChain                bool
	Office                                          string
	ToolRank                                        []engine.Tool
}

func idsToTools(ids ...string) []engine.Tool {
	out := make([]engine.Tool, 0, len(ids))
	for _, id := range ids {
		if t, ok := engine.ToolByID(id); ok {
			out = append(out, t)
		}
	}
	return out
}

var defaultRank = idsToTools("crown", "rope", "trap", "claws", "maul", "scapegoat", "coin", "maiden", "shackles", "muzzle", "ravenous", "blacklist", "revenge", "skewer", "hook", "stake", "collar", "saw", "guillotine", "dogtag", "tongs", "whip", "keys", "cage", "scales", "pestle", "shortchain", "fire", "axe", "forged", "ironbox")

func defaults() Weights {
	return Weights{
		TierBase: 3, RainbowMult: 2, WOpen: 1, WNow: 1, WWalk: 1, WNest: -6, WChain: 4, WGoal: 1, WInstigate: 0.5, WCrow: 2,
		WShort: 0.1, WCage: 1, RerollMargin: 0.02, MaidenSwapTier: 3, AcceptChain: true, ToolSets: true, GiftChain: true, ToolRank: defaultRank,
	}
}

// Profiles are the tuned weights for runs with tool rewards on and off.
var (
	ProfileRewards = func() Weights {
		w := defaults()
		w.TierBase, w.RainbowMult, w.WOpen, w.WNow, w.WWalk, w.WStep, w.WChain, w.WStair = 3, 1.5, 1.5, 0.844, 0.75, 0.5, 6, 0.25
		w.SwapMaxTier, w.WCrow, w.WIncident, w.WPass, w.WOfficeIncident, w.WConsume, w.WKeys = 3, 1, 40, 40, 60, 20, 1
		return w
	}()
	ProfileNoTools = func() Weights {
		w := defaults()
		w.TierBase, w.RainbowMult, w.WOpen, w.WNow, w.WWalk, w.WStep, w.WChain, w.WStair = 2.75, 12, 2.25, 0.375, 1.875, 0.5, 24, 0.125
		w.SwapMaxTier, w.ExecMargin, w.AcceptChain, w.WCrow = 1, -5, false, 1
		return w
	}()
)

func paramsFor(st *engine.State) *Weights {
	if st.Params.Rewards {
		return &ProfileRewards
	}
	return &ProfileNoTools
}

// diet is a bitmask of beasts per eater.
type diet [4]uint8

type dist [6]float64

type memoKey struct {
	lo, hi uint64
	meta   uint32
}

type evalCtx struct {
	st                                             *engine.State
	P                                              *Weights
	eats, counters, eatsHigh, countersHigh         diet
	doubleFrom                                     int
	fight                                          bool
	v                                              [6]float64
	R                                              float64
	saw, whip, muzzle, skewer, crown, maul, collar bool
	revenge, dogtag, cage, blacklist, shortchain   bool
	keys, forged, fire, stake, rope                bool
	axeDoors                                       int
	maiden                                         bool
	maidenCounts                                   [5]int
	scapegoat                                      bool
	scapegoatLoss                                  float64
	swapCap                                        int
	sealed                                         int
	lift, lift2, allLift2, shacklesLift            float64
	boardCount                                     int
	goalLeft, incidentsLeft, consumesLeft          int
	scratchExtra                                   int
	openDist                                       dist
	openValue                                      float64
	chainStop                                      bool
	memo                                           map[memoKey]float64
}

func normalize(w []float64) dist {
	var out dist
	total := 0.0
	for _, x := range w {
		if x > 0 {
			total += x
		}
	}
	if total == 0 {
		total = 1
	}
	for i, x := range w {
		if x > 0 {
			out[i] = x / total
		}
	}
	return out
}

func floorDist(d dist, floor int) dist {
	var kept dist
	any := false
	for t, p := range d {
		if t >= floor {
			kept[t] = p
			any = any || p > 0
		}
	}
	if !any {
		return d
	}
	return normalize(kept[:])
}

func composition(st *engine.State, exclude int) (comp [24]int8, crowd int) {
	for n := range st.Cells {
		c := &st.Cells[n]
		if c.Sealed || !c.HasInmate {
			continue
		}
		crowd++
		if n == exclude || c.Inmate.Tier >= rainbow {
			continue
		}
		comp[int(c.Inmate.Tier)*4+int(c.Inmate.Beast)]++
	}
	return
}

func newContext(st *engine.State, P *Weights) *evalCtx {
	c := &evalCtx{st: st, P: P}
	for b := 0; b < 4; b++ {
		c.eats[b] = 1 << engine.FoodChain[b]
	}
	if st.Has(engine.ToolSkewer) {
		c.eats[engine.Rat] |= 1 << engine.Rat
	}
	c.counters = c.eats
	if st.Has(engine.ToolCrown) {
		c.eats[engine.Lion] = 0b1111
	}
	c.eatsHigh, c.countersHigh = c.eats, c.counters
	for b := 0; b < 4; b++ {
		if s := engine.SecondMeal[b]; s >= 0 {
			c.eatsHigh[b] |= 1 << uint(s)
			c.countersHigh[b] |= 1 << uint(s)
		}
	}
	c.fight = st.CageFight()
	c.doubleFrom = st.Params.DoubleEatTier
	if c.fight {
		c.doubleFrom = 0
	}
	for t := range c.v {
		c.v[t] = math.Pow(P.TierBase, float64(t))
	}
	w := st.Params.TierWeights
	if st.Has(engine.ToolHook) {
		for _, x := range w[1:] {
			if x > 0 {
				w[0] = 0
				break
			}
		}
	}
	c.openDist = normalize(w[:])
	if st.VaultN >= engine.VaultSlots {
		c.R = c.v[engine.Supreme]
	} else {
		c.R = c.v[rainbow] * P.RainbowMult
	}
	for n := range st.Cells {
		if st.Cells[n].Sealed {
			c.sealed++
		}
	}
	var board, board2 float64
	var tierCount [5]int
	for n := range st.Cells {
		cl := &st.Cells[n]
		if cl.Sealed || !cl.HasInmate || cl.Inmate.Tier >= rainbow {
			continue
		}
		t := int(cl.Inmate.Tier)
		c.boardCount++
		tierCount[t]++
		board += c.tierVal(t+1) - c.v[t]
		board2 += c.tierVal(t+2) - c.v[t]
	}
	if c.boardCount > 0 {
		c.lift = board / float64(c.boardCount)
		c.lift2 = board2 / float64(c.boardCount)
	}
	c.allLift2 = board2
	has := st.Has
	c.saw, c.whip, c.muzzle = has(engine.ToolSaw), has(engine.ToolWhip), has(engine.ToolMuzzle)
	c.skewer, c.crown, c.maul, c.collar = has(engine.ToolSkewer), has(engine.ToolCrown), has(engine.ToolMaul), has(engine.ToolCollar)
	c.revenge = has(engine.ToolRevenge) && c.sealed > 0
	c.dogtag, c.cage, c.blacklist, c.shortchain = has(engine.ToolDogtag), has(engine.ToolCage), has(engine.ToolBlacklist), has(engine.ToolShortchain)
	c.keys, c.forged, c.fire, c.stake, c.rope = has(engine.ToolKeys), has(engine.ToolForged), has(engine.ToolFire), has(engine.ToolStake), has(engine.ToolRope)
	if has(engine.ToolAxe) {
		c.axeDoors = min(2, c.sealed)
	}
	if has(engine.ToolMaiden) {
		c.maiden, c.maidenCounts = true, tierCount
	}
	if has(engine.ToolScapegoat) && c.boardCount >= 3 {
		c.scapegoat = true
		sum := 0.0
		for t, k := range tierCount {
			sum += float64(k) * c.v[t]
		}
		c.scapegoatLoss = 2 * sum / float64(c.boardCount)
	}
	switch {
	case P.SwapMaxTier < 0:
		c.swapCap = -1
	case c.scapegoat:
		c.swapCap = engine.Supreme
	default:
		m := P.SwapMaxTier
		if c.maiden {
			m = max(m, P.MaidenSwapTier)
		}
		if c.stake {
			m = max(m, 2)
		}
		c.swapCap = m
	}
	if has(engine.ToolShackles) && c.boardCount > 0 {
		c.shacklesLift = c.lift
	}
	rewards := st.Params.Rewards && !st.Milestone
	if rewards {
		switch st.Objective {
		case engine.ObjWalk:
			c.goalLeft = engine.ObjectiveNeed[engine.ObjWalk] - st.Progress
		case engine.ObjIncident:
			c.incidentsLeft = engine.ObjectiveNeed[engine.ObjIncident] - st.Progress
		case engine.ObjConsume:
			c.consumesLeft = engine.ObjectiveNeed[engine.ObjConsume] - st.Progress
		}
	}
	c.scratchExtra = 1
	if st.Params.ScratchStacksBase {
		c.scratchExtra = 2
	}
	for t, p := range c.openDist {
		c.openValue += p * c.v[t]
	}
	c.chainStop = st.CanStopChain()
	return c
}

// tierVal is a tier's value, with Rainbow worth the vault's R.
func (c *evalCtx) tierVal(t int) float64 {
	if t >= rainbow {
		return c.R
	}
	return c.v[t]
}

func (c *evalCtx) eatsAt(t int) *diet {
	if t >= c.doubleFrom {
		return &c.eatsHigh
	}
	return &c.eats
}

func (c *evalCtx) countersAt(t int) *diet {
	if t >= c.doubleFrom {
		return &c.countersHigh
	}
	return &c.counters
}

func (c *evalCtx) topMeal(t, b int) int {
	if c.maul && b == int(engine.Mammoth) {
		return rainbow - 1
	}
	return t
}

func (c *evalCtx) firstMeal(t int) int {
	if c.fight {
		return 0
	}
	return t
}

func (c *evalCtx) mealDiet(mt, t int) *diet {
	if mt != t {
		return c.countersAt(t)
	}
	return c.eatsAt(t)
}

// ---------- Chain math ----------

func swapDist(c *evalCtx, oldTier int) dist {
	w := c.st.Params.SwapWeights
	if c.rope {
		w[0], w[1] = 0, 0
	}
	var d dist
	if c.scapegoat {
		var higher [5]float64
		any := false
		for t := range w {
			if t > oldTier {
				higher[t] = w[t]
				any = any || w[t] > 0
			}
		}
		if any {
			d = normalize(higher[:])
		} else {
			d[min(rainbow, oldTier+1)] = 1
		}
	} else {
		d = normalize(w[:])
	}
	if c.stake && oldTier < 3 {
		for t := range d {
			d[t] *= 0.6
		}
		d[3] += 0.4
	}
	return d
}

func rise(c *evalCtx, t int, comp *[24]int8, crowd, keep int) float64 {
	if t >= rainbow {
		return c.R
	}
	first, last := 0, 3
	if keep >= 0 {
		first, last = keep, keep
	}
	sum := 0.0
	for b := first; b <= last; b++ {
		if c.revenge {
			foods := c.eatsAt(t)[b]
			spawned, nf := 0.0, 0
			for f := 0; f < 4; f++ {
				if foods>>uint(f)&1 == 0 {
					continue
				}
				nf++
				comp[t*4+f]++
				spawned += c.v[t] + feed(c, t, b, comp, crowd+1)
				comp[t*4+f]--
			}
			if nf > 0 {
				sum += 0.3*spawned/float64(nf) + 0.7*feed(c, t, b, comp, crowd)
			} else {
				sum += feed(c, t, b, comp, crowd)
			}
		} else {
			sum += feed(c, t, b, comp, crowd)
		}
	}
	return sum / float64(last-first+1)
}

func feed(c *evalCtx, t, b int, comp *[24]int8, crowd int) float64 {
	i0 := c.firstMeal(t) * 4
	key := memoKey{meta: uint32(t) | uint32(b)<<4}
	if c.muzzle {
		key.meta |= uint32(crowd+1) << 8
	}
	for i := i0; i < 20; i++ {
		k := uint(i-i0) * 4
		if k < 64 {
			key.lo |= uint64(comp[i]) << k
		} else {
			key.hi |= uint64(comp[i]) << (k - 64)
		}
	}
	if v, ok := c.memo[key]; ok {
		return v
	}
	out := -inf
	for mt := c.firstMeal(t); mt <= c.topMeal(t, b); mt++ {
		d := c.mealDiet(mt, t)[b]
		for f := 0; f < 4; f++ {
			if d>>uint(f)&1 == 0 || comp[mt*4+f] == 0 {
				continue
			}
			comp[mt*4+f]--
			out = math.Max(out, biteValue(c, t, b, comp, crowd, 0, true, mt))
			comp[mt*4+f]++
		}
	}
	if out == -inf || (c.chainStop && out-c.v[t] < c.P.StopMargin) {
		out = c.v[t]
	}
	if c.memo == nil {
		c.memo = make(map[memoKey]float64, 64)
	}
	c.memo[key] = out
	return out
}

func biteValue(c *evalCtx, t, b int, comp *[24]int8, crowd, extra int, chained bool, mealTier int) float64 {
	gain := 1 + extra
	if c.maul && b == int(engine.Mammoth) {
		gain = max(1, mealTier+2-t) + extra
	}
	if c.muzzle && crowd-1 <= 4 {
		gain += 2
	}
	if c.skewer && b == int(engine.Rat) {
		gain++
	}
	if c.crown && b == int(engine.Lion) {
		gain += 2
	}
	keep := -1
	if c.fight {
		keep = b
	}
	after := func(g int) float64 { return rise(c, min(rainbow, t+g), comp, crowd-1, keep) }
	s, w := 0.0, 0.0
	if c.saw {
		s = 0.25
	}
	if c.whip {
		w = 0.35
	}
	sawed := func(g int) float64 {
		if s == 0 && w == 0 {
			return after(g)
		}
		return (1-s)*(1-w)*after(g) + (s+w-2*s*w)*after(g+1) + s*w*after(g+2)
	}
	if chained && c.collar {
		return -c.v[mealTier] + 0.2*sawed(gain) + 0.8*sawed(gain+1)
	}
	return -c.v[mealTier] + sawed(gain)
}

func devourDelta(c *evalCtx, tier, beast, meal int, scratch bool, comp *[24]int8, crowd int, chained bool, mealTier int) float64 {
	i := mealTier*4 + meal
	if !scratch {
		comp[i]--
	}
	cr, extra := crowd, 0
	if scratch {
		cr, extra = crowd+1, c.scratchExtra
	}
	value := biteValue(c, tier, beast, comp, cr, extra, chained, mealTier) - c.v[tier]
	if !scratch {
		comp[i]++
	}
	return value
}

func bestMeal(c *evalCtx, tier, beast int, comp *[24]int8, crowd int) float64 {
	best := -inf
	for mt := c.firstMeal(tier); mt <= c.topMeal(tier, beast); mt++ {
		d := c.mealDiet(mt, tier)[beast]
		for f := 0; f < 4; f++ {
			if d>>uint(f)&1 == 0 || comp[mt*4+f] == 0 {
				continue
			}
			best = math.Max(best, devourDelta(c, tier, beast, f, false, comp, crowd, false, mt))
		}
	}
	return best
}

// ChainOdds is the exact chance a consume's chain reaches Rainbow and its
// expected bites, picking each later meal for the best odds (no tools).
func ChainOdds(st *engine.State, from, to int) (rainbowOdds, bites float64) {
	w := Weights{TierBase: 1, RainbowMult: 1}
	c := newContext(st, &w)
	eater := st.Cells[from].Inmate
	target := &st.Cells[to]
	meal := target.Inmate
	if target.Sealed {
		meal = target.Hidden
	}
	comp, _ := composition(st, from)
	if !target.Sealed {
		comp[int(meal.Tier)*4+int(meal.Beast)]--
	}
	type pn struct{ p, n float64 }
	memo := map[memoKey]pn{}
	var fromTier func(t, keep int) pn
	var eat func(t, b int) pn
	fromTier = func(t, keep int) pn {
		if t >= rainbow {
			return pn{1, 0}
		}
		first, last := 0, 3
		if keep >= 0 {
			first, last = keep, keep
		}
		var out pn
		k := float64(last - first + 1)
		for b := first; b <= last; b++ {
			r := eat(t, b)
			out.p += r.p / k
			out.n += r.n / k
		}
		return out
	}
	eat = func(t, b int) pn {
		i0 := c.firstMeal(t) * 4
		key := memoKey{meta: uint32(t) | uint32(b)<<4}
		for i := i0; i < 20; i++ {
			k := uint(i-i0) * 4
			if k < 64 {
				key.lo |= uint64(comp[i]) << k
			} else {
				key.hi |= uint64(comp[i]) << (k - 64)
			}
		}
		if v, ok := memo[key]; ok {
			return v
		}
		var out *pn
		for mt := c.firstMeal(t); mt <= c.topMeal(t, b); mt++ {
			d := c.mealDiet(mt, t)[b]
			for f := 0; f < 4; f++ {
				if d>>uint(f)&1 == 0 || comp[mt*4+f] == 0 {
					continue
				}
				comp[mt*4+f]--
				nt := t + 1
				if c.maul && b == int(engine.Mammoth) {
					nt = max(t+1, mt+2)
				}
				keep := -1
				if c.fight {
					keep = b
				}
				r := fromTier(nt, keep)
				comp[mt*4+f]++
				if out == nil || r.p > out.p+1e-12 || (math.Abs(r.p-out.p) <= 1e-12 && r.n+1 > out.n) {
					out = &pn{r.p, r.n + 1}
				}
			}
		}
		res := pn{}
		if out != nil {
			res = *out
		}
		memo[key] = res
		return res
	}
	gain := 1
	if target.Sealed && target.Scratch {
		gain = 1 + c.scratchExtra
	} else if c.maul && eater.Beast == engine.Mammoth {
		gain = max(1, int(meal.Tier)+2-int(eater.Tier))
	}
	keep := -1
	if c.fight {
		keep = int(eater.Beast)
	}
	r := fromTier(min(rainbow, int(eater.Tier)+gain), keep)
	return r.p, 1 + r.n
}
