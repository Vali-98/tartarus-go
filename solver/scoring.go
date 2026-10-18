package solver

import (
	"embed"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"tartarus/engine"
)

// ---------- Swap, event and office values ----------

func swapValue(c *evalCtx, tier int) float64 {
	d := swapDist(c, tier)
	value := -c.v[tier]
	above := 0.0
	for t, p := range d {
		value += p * c.tierVal(t)
		if t > tier {
			above += p
		}
	}
	if c.axeDoors > 0 {
		value += above * float64(c.axeDoors) * c.P.WOpen * c.openValue
	}
	if c.scapegoat {
		value -= c.scapegoatLoss
	}
	_, mv := maidenBonus(c, tier)
	return value + mv
}

// maidenBonus: the chance a swap's newcomer has as many inmates above as
// below its rarity, and what its 3 extra tiers add on average.
func maidenBonus(c *evalCtx, tier int) (chance, value float64) {
	if !c.maiden {
		return
	}
	d := swapDist(c, tier)
	counts := c.maidenCounts
	if counts[tier] > 0 {
		counts[tier]--
	}
	for t, p := range d {
		if p == 0 {
			continue
		}
		above, below := 0, 0
		for u, n := range counts {
			if u > t {
				above += n
			} else if u < t {
				below += n
			}
		}
		if above != below {
			continue
		}
		chance += p
		value += p * (c.tierVal(t+3) - c.tierVal(t))
	}
	return
}

func tricksterValue(c *evalCtx, d dist) float64 {
	sum := 0.0
	for t, p := range d {
		sum += p * math.Max(0, c.v[engine.Supreme]-c.v[min(t, engine.Supreme)])
	}
	return sum
}

func appetiteValue(c *evalCtx, low int, d dist) float64 {
	n, q := engine.AppetiteTries, engine.AppetiteOdds
	sum := 0.0
	for t, p := range d {
		if p == 0 {
			continue
		}
		from := min(t, low)
		choose := 1.0
		for k := 0; k <= n; k++ {
			if k > 0 {
				choose = choose * float64(n-k+1) / float64(k)
			}
			sum += p * choose * math.Pow(q, float64(k)) * math.Pow(1-q, float64(n-k)) * (c.tierVal(min(rainbow, from+k)) - c.tierVal(from))
		}
	}
	return sum
}

func activation(c *evalCtx, tier, beast int, comp *[24]int8, crowd int, swapLeft bool) float64 {
	value := 0.0
	if tier < rainbow {
		value = math.Max(value, bestMeal(c, tier, beast, comp, crowd))
	}
	if swapLeft && tier <= c.swapCap {
		value = math.Max(value, swapValue(c, tier))
	}
	return value
}

func freshActivation(c *evalCtx, d dist, comp *[24]int8, crowd int, swapLeft bool) float64 {
	sum := 0.0
	for t, p := range d {
		if p == 0 {
			continue
		}
		for b := 0; b < 4; b++ {
			sum += p / 4 * activation(c, t, b, comp, crowd, swapLeft)
		}
	}
	return sum
}

func tagged(c *evalCtx, d dist) dist {
	if !c.dogtag {
		return d
	}
	var out dist
	for t, p := range d {
		out[t] += 0.75 * p
		out[min(4, t+1)] += 0.25 * p
	}
	return out
}

func stair(c *evalCtx, comp *[24]int8) float64 {
	value := 0.0
	for t := 0; t < rainbow; t++ {
		var present uint8
		for f := 0; f < 4; f++ {
			if comp[t*4+f] > 0 {
				present |= 1 << uint(f)
			}
		}
		fed := 0
		for _, d := range c.eatsAt(t) {
			if d&present != 0 {
				fed++
			}
		}
		value += float64(fed) / 4 * c.v[t]
	}
	return value
}

func stairDelta(c *evalCtx, comp *[24]int8, tier, beast, sign int) float64 {
	if c.P.WStair == 0 || tier >= rainbow {
		return 0
	}
	before := stair(c, comp)
	comp[tier*4+beast] += int8(sign)
	after := stair(c, comp)
	comp[tier*4+beast] -= int8(sign)
	return c.P.WStair * (after - before)
}

func stairFresh(c *evalCtx, d dist, comp *[24]int8) float64 {
	if c.P.WStair == 0 {
		return 0
	}
	sum := 0.0
	for t, p := range d {
		if p == 0 {
			continue
		}
		for b := 0; b < 4; b++ {
			sum += p / 4 * stairDelta(c, comp, t, b, 1)
		}
	}
	return sum
}

func freshValue(c *evalCtx, d dist, comp *[24]int8, crowd int, swapLeft bool) float64 {
	worth := 0.0
	for t, p := range d {
		worth += p * c.v[t]
	}
	return c.P.WOpen*worth + c.P.WNow*freshActivation(c, d, comp, crowd, swapLeft) + stairFresh(c, d, comp)
}

func executionValue(c *evalCtx, tier, beast int, comp *[24]int8) float64 {
	same := c.st.Params.ExecutionSameTier
	prey := int(engine.FoodChain[beast])
	count, lost := 0, 0.0
	top := tier - 1
	if same {
		top = tier
	}
	for t := 0; t <= top; t++ {
		if same && t != tier {
			continue
		}
		count += int(comp[t*4+prey])
		lost += float64(comp[t*4+prey]) * c.v[t]
	}
	if count == 0 {
		return 0
	}
	return c.tierVal(min(rainbow, tier+count)) - c.v[tier] - lost
}

var eventWhy = map[engine.Event]string{
	engine.EvExecution: "an Execution waits behind the door",
	engine.EvChain:     "a Chain reaction waits behind the door",
	engine.EvTrickster: "Behind Closed Doors waits there: whoever is inside becomes a Supreme",
	engine.EvAppetite:  "Ravenous Appetite waits there: the lowest-rarity inmate gets 5 tries at +1 tier",
	engine.EvConvict:   "High-Risk Convict waits there: another cell opens on a Legendary",
	engine.EvCrow:      "Crow Whispers waits there: it shows the rarity behind the next 4 locked doors",
	engine.EvFreedom:   "Taste of Freedom waits there: 3 more locked cells open",
	engine.EvUsurp1:    "Usurpation I waits there: a random inmate rises a tier, and Usurpation II appears",
	engine.EvUsurp2:    "Usurpation II waits there: 2 random inmates rise a tier",
	engine.EvUsurp3:    "Usurpation III waits there: 2 random inmates rise 2 tiers",
	engine.EvUsurp4:    "Usurpation IV waits there: every inmate rises 2 tiers",
	engine.EvPortion:   "Extra Portion waits there: up to 3 random inmates rise a tier",
	engine.EvRatway:    "the Sewer Ratway waits there: a free move to any cell but the office",
	engine.EvHunt:      "\"Treasure\" Hunt waits there: the doors left dark hold Rare or better",
}

func eventWorth(c *evalCtx, ev engine.Event, comp *[24]int8) float64 {
	P := c.P
	switch ev {
	case engine.EvExecution:
		exec := 0.0
		for t := 0; t < 5; t++ {
			for b := 0; b < 4; b++ {
				exec += c.openDist[t] / 4 * math.Max(0, executionValue(c, t, b, comp)-P.ExecMargin)
			}
		}
		return exec
	case engine.EvChain:
		return P.WChain
	case engine.EvTrickster:
		return tricksterValue(c, c.openDist)
	case engine.EvAppetite:
		low := 99
		for i := 0; i < 20; i++ {
			if comp[i] != 0 {
				low = i / 4
				break
			}
		}
		return appetiteValue(c, low, c.openDist)
	case engine.EvConvict:
		return P.WOpen * c.v[3]
	case engine.EvCrow:
		return P.WCrow
	case engine.EvFreedom:
		return engine.FreedomCells * P.WOpen * c.openValue
	case engine.EvUsurp1:
		return c.lift + 2*c.lift*P.WInstigate
	case engine.EvUsurp2:
		return 2*c.lift + 2*c.lift2*P.WInstigate
	case engine.EvUsurp3:
		return 2*c.lift2 + c.allLift2*P.WInstigate
	case engine.EvUsurp4:
		return c.allLift2
	case engine.EvPortion:
		return float64(min(engine.PortionInmates, c.boardCount)) * c.lift
	case engine.EvRatway:
		return P.WOpen * c.openValue
	case engine.EvHunt:
		return P.WCrow * math.Max(1, float64(c.sealed)/engine.CrowCells)
	}
	return 0
}

func (c *evalCtx) anyEventWorth(comp *[24]int8) float64 {
	sum := 0.0
	for _, e := range c.st.Pool[:c.st.PoolN] {
		sum += eventWorth(c, e, comp)
	}
	return sum / float64(c.st.PoolN)
}

type officeVals struct{ bestow, instigate, tyranny float64 }

func officeValues(st *engine.State, c *evalCtx, comp *[24]int8) officeVals {
	bestow, count := 0.0, 0
	for n := range st.Cells {
		cl := &st.Cells[n]
		if cl.Sealed || !cl.HasInmate || cl.Inmate.Tier >= rainbow {
			continue
		}
		t := int(cl.Inmate.Tier)
		bestow += c.tierVal(t+1) - c.v[t]
		count++
	}
	bestow /= float64(max(1, count))
	free := len(st.InstigateCells())
	each := c.anyEventWorth(comp)
	extra := 0
	if st.StartEvent == engine.StartTyranny {
		extra = 3
	}
	first := st.InstigateCount()
	tyranny := 0.0
	if extra > 0 {
		tyranny = float64(extra)*bestow + float64(min(first+(extra-1)*engine.InstigateEvents, free))*each*c.P.WInstigate
	}
	made := first
	if extra > 0 {
		made = engine.InstigateEvents
	}
	made = min(made, free)
	goal := 0.0
	if c.incidentsLeft > 0 {
		goal = c.P.WOfficeIncident * float64(min(made, c.incidentsLeft)) / float64(c.incidentsLeft)
	}
	return officeVals{bestow, float64(made)*each*c.P.WInstigate + goal, tyranny}
}

// ---------- Patrol scores ----------

type PatrolScore struct {
	Cell  int
	Score float64
	Why   string
}

func patrolScores(st *engine.State, c *evalCtx) []PatrolScore {
	P := c.P
	comp, crowd := composition(st, -1)
	swapLeft := st.CanSwapNow()
	fresh, haveFresh := 0.0, false
	freshNow := func() float64 {
		if !haveFresh {
			fresh, haveFresh = freshValue(c, tagged(c, c.openDist), &comp, crowd+1, swapLeft), true
		}
		return fresh
	}
	incidentAt := func(cl *engine.Cell) bool { return cl.Event != engine.EvNone && (cl.Sealed || cl.HasInmate) }
	known := func(cl *engine.Cell) float64 {
		cp, cr := composition(st, int(cl.N))
		return activation(c, int(cl.Inmate.Tier), int(cl.Inmate.Beast), &cp, cr, swapLeft)
	}
	rng := min(st.Params.PatrolRange, engine.CellCount-1)
	var out []PatrolScore
	for _, n := range st.PatrolOptions() {
		cl := &st.Cells[n]
		steps := 0
		if !st.Ratway && engine.StepsBetween(st.Position, n) <= rng {
			steps = engine.StepsBetween(st.Position, n)
		}
		score := P.WStep * float64(steps)
		var why string
		switch {
		case st.Chain && !st.Ratway:
			path := []int{n}
			if steps > 0 {
				path = st.ClockwisePath(n)
			}
			sealed := 0
			for _, k := range path {
				if st.Cells[k].Sealed {
					sealed++
				}
			}
			dec := 0
			if cl.Sealed {
				dec = 1
			}
			score += P.WOpen * float64(sealed-dec) * c.openValue
			switch {
			case cl.Sealed:
				score += freshNow()
			case cl.HasInmate:
				score += P.WWalk * known(cl)
			}
			why = fmt.Sprintf("the chain opens %s on the way", plural(sealed, "door"))
		case cl.Sealed && cl.Nest:
			score += P.WNest
			why = fmt.Sprintf("it destroys the rat nest due on day %d", cl.NestDue)
		case cl.Sealed:
			var d dist
			haveDist := false
			if cl.Known > 0 && cl.HasHidden {
				d[min(4, int(cl.Hidden.Tier))], haveDist = 1, true
			} else if cl.AtLeast > 0 {
				d, haveDist = floorDist(c.openDist, int(cl.AtLeast)), true
			}
			if haveDist {
				score += freshValue(c, tagged(c, d), &comp, crowd+1, swapLeft)
			} else {
				score += freshNow()
			}
			if c.blacklist && c.sealed-1 <= 5 {
				score += 2 * c.lift
			}
			if c.keys && P.WKeys != 0 {
				beside := 0
				for _, k := range [2]int{n + engine.CellCount - 1, n + 1} {
					if st.Cells[k%engine.CellCount].Sealed {
						beside++
					}
				}
				score += P.WKeys * 0.8 * float64(beside) * P.WOpen * c.openValue
			}
			if c.forged && P.WForged != 0 && c.sealed > 1 {
				score += P.WForged * 0.5 * P.WOpen * c.openValue
			}
			if c.fire && P.WFire != 0 && c.sealed > 1 {
				score += P.WFire * 0.35 * P.WOpen * c.openValue
			}
			why = "a new inmate, who may consume today"
			if cl.Known > 0 && cl.HasHidden {
				why = fmt.Sprintf("a %s inmate inside, who may consume today", engine.TierNames[cl.Hidden.Tier])
			} else if cl.AtLeast > 0 {
				why = fmt.Sprintf("a %s or better inmate (the Hunt left this door dark)", engine.TierNames[cl.AtLeast])
			}
			if cl.Event != engine.EvNone {
				if cl.EventUnknown {
					score += c.anyEventWorth(&comp)
					why = "a special event waits behind the door"
				} else {
					score += eventWorth(c, cl.Event, &comp)
					why = eventWhy[cl.Event]
				}
			}
		case n == engine.Office:
			o := officeValues(st, c, &comp)
			score += math.Max(o.bestow, o.instigate) + o.tyranny
			if o.bestow >= o.instigate {
				why = "the office: Bestow raises 1 random inmate a tier"
			} else {
				why = "the office: Instigate brings new special events"
			}
		case cl.HasInmate:
			score += P.WWalk * known(cl)
			why = fmt.Sprintf("its %s can act today", engine.Describe(cl.Inmate))
			if c.consumesLeft > 0 && P.WConsumeWalk != 0 && len(st.DevourOptions(n)) > 0 {
				score += P.WConsumeWalk / float64(c.consumesLeft)
				why += ", and its consume counts for Survival of the Fittest"
			}
			if cl.Event != engine.EvNone {
				if cl.EventUnknown {
					score += c.anyEventWorth(&comp)
					why = "a special event waits in the cell"
				} else {
					score += eventWorth(c, cl.Event, &comp)
					why = strings.Replace(eventWhy[cl.Event], "behind the door", "in the cell", 1)
				}
			}
		default:
			why = "an empty cell"
		}
		if c.shacklesLift != 0 && steps >= 4 {
			score += 0.4 * c.shacklesLift
		}
		if c.shortchain && steps >= 1 && steps <= 3 {
			need := 0
			if cl.Sealed {
				need = 1
			}
			if c.sealed > need {
				score += P.WShort * 0.5 * P.WOpen * c.openValue
			}
		}
		if c.goalLeft > 0 {
			score += P.WGoal * float64(min(steps, c.goalLeft))
		}
		if c.incidentsLeft > 0 {
			if incidentAt(cl) {
				score += P.WIncident / float64(c.incidentsLeft)
				if P.WIncident != 0 {
					why += ", and it counts for Undercurrents"
				}
			}
			if steps > 1 && P.WPass != 0 {
				pass := 0
				for _, k := range st.ClockwisePath(n) {
					if k != n && incidentAt(&st.Cells[k]) {
						pass++
					}
				}
				score -= P.WPass * float64(pass) / float64(c.incidentsLeft)
			}
		}
		out = append(out, PatrolScore{n, score, why})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

func plural(n int, w string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, w)
	}
	return fmt.Sprintf("%d %ss", n, w)
}

// ---------- Consume, swap, hunt ----------

func linkValue(st *engine.State, c *evalCtx, l *engine.Prey) float64 {
	value := 0.0
	for _, id := range [2]int32{l.Lion, l.Prey} {
		for n := range st.Cells {
			cl := &st.Cells[n]
			if cl.HasInmate && cl.Inmate.ID == id {
				t := int(cl.Inmate.Tier)
				value += c.tierVal(min(rainbow, t+l.Rounds)) - c.v[t]
			}
		}
	}
	return value
}

func linkLoss(st *engine.State, c *evalCtx, in engine.Inmate, certain bool) float64 {
	l := engine.LinkOf(st, in.ID)
	if l == nil {
		return 0
	}
	f := 0.75
	if certain || l.Prey == in.ID {
		f = 1
	}
	return linkValue(st, c, l) * f
}

type scored struct {
	cell  int
	score float64
}

func devourScores(st *engine.State, c *evalCtx) []scored {
	here := &st.Cells[st.Position]
	if here.Sealed || !here.HasInmate {
		return nil
	}
	comp, crowd := composition(st, st.Position)
	tier, beast := int(here.Inmate.Tier), int(here.Inmate.Beast)
	own := linkLoss(st, c, here.Inmate, false)
	chained := st.Chaining.Active
	opts := st.StartOptions()
	if chained {
		opts = st.ChainOptions()
	}
	out := make([]scored, 0, len(opts))
	for _, o := range opts {
		target := &st.Cells[o.Cell]
		meal := target.Inmate
		if o.Scratch {
			meal = target.Hidden
		}
		mealLink := 0.0
		if !o.Scratch {
			mealLink = linkLoss(st, c, meal, true)
		}
		loss := own + mealLink
		if own != 0 && mealLink != 0 && engine.LinkOf(st, here.Inmate.ID) == engine.LinkOf(st, meal.ID) {
			loss = math.Max(own, mealLink)
		}
		stairLoss := 0.0
		if !o.Scratch {
			stairLoss = stairDelta(c, &comp, int(meal.Tier), int(meal.Beast), -1)
		}
		cage := 0.0
		if c.cage {
			cage = c.P.WCage * 0.4 * c.P.WOpen * c.openValue
		}
		mt := int(meal.Tier)
		if o.Scratch {
			mt = tier
		}
		out = append(out, scored{o.Cell, devourDelta(c, tier, beast, int(meal.Beast), o.Scratch, &comp, crowd, chained, mt) - loss + stairLoss + cage})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	return out
}

func swapGainHere(st *engine.State, c *evalCtx) float64 {
	here := &st.Cells[st.Position]
	if here.Sealed || !here.HasInmate || !st.CanSwapNow() || c.swapCap < 0 {
		return -inf
	}
	gain := swapValue(c, int(here.Inmate.Tier)) - linkLoss(st, c, here.Inmate, true)
	if here.Ivory {
		lift, n := 0.0, 0
		for i := range st.Cells {
			cl := &st.Cells[i]
			if !cl.Sealed && cl.HasInmate && cl.Inmate.Tier < rainbow {
				lift += c.v[cl.Inmate.Tier+1] - c.v[cl.Inmate.Tier]
				n++
			}
		}
		lift /= float64(max(1, n))
		return gain + float64(min(st.Params.IvoryCount, n))*lift
	}
	if int(here.Inmate.Tier) <= c.swapCap {
		return gain
	}
	return -inf
}

func bestHunt(st *engine.State, c *evalCtx) (cell int, value float64, ok bool) {
	if !st.CanHuntNow() {
		return
	}
	lion := st.Cells[st.Position].Inmate
	for _, n := range st.HuntTargets() {
		l := engine.Prey{Lion: lion.ID, Prey: st.Cells[n].Inmate.ID, Rounds: st.Params.PreyRounds}
		v := 0.8 * linkValue(st, c, &l)
		if !ok || v > value {
			cell, value, ok = n, v, true
		}
	}
	return
}

// ---------- Gifts ----------

func boardValue(st *engine.State, c *evalCtx) float64 {
	value := float64(st.VaultN) * c.R
	for n := range st.Cells {
		cl := &st.Cells[n]
		if cl.Sealed || !cl.HasInmate {
			continue
		}
		f := 1.0
		if len(st.DevourOptions(n)) > 0 {
			f = 1.25
		}
		value += c.v[cl.Inmate.Tier] * f
	}
	return value
}

func pickGift(st *engine.State, c *evalCtx, options []uint8, rnd *rng32) uint8 {
	has := func(g engine.Gift) bool {
		for _, o := range options {
			if o == uint8(g) {
				return true
			}
		}
		return false
	}
	if c.P.GiftChain {
		left := 0
		for _, p := range st.Pending {
			if p.Gift {
				left++
			}
		}
		if has(engine.GiftHorn) {
			for i := 0; i < 3; i++ {
				trial := st.Clone()
				trial.Rng = rnd.next32()
				if trial.Choose(uint8(engine.GiftHorn)).OK && trial.VaultN > st.VaultN {
					return uint8(engine.GiftHorn)
				}
			}
		}
		if left >= 3 && has(engine.GiftRing) {
			for n := 1; n < engine.CellCount; n++ {
				if st.Cells[n].Sealed {
					return uint8(engine.GiftRing)
				}
			}
		}
		if left >= 2 && has(engine.GiftWine) {
			for n := range st.Cells {
				cl := &st.Cells[n]
				if !cl.Sealed && cl.HasInmate && cl.Inmate.Tier < engine.Supreme-1 {
					return uint8(engine.GiftWine)
				}
			}
		}
	}
	best, bestV := options[0], -inf
	for _, id := range options {
		samples := 1
		if id == uint8(engine.GiftRing) || id == uint8(engine.GiftHorn) {
			samples = 3
		}
		sum, got := 0.0, false
		for i := 0; i < samples; i++ {
			trial := st.Clone()
			trial.Rng = rnd.next32()
			if !trial.Choose(id).OK {
				continue
			}
			got = true
			sum += boardValue(trial, newContext(trial, c.P))
		}
		if !got {
			continue
		}
		if v := sum / float64(samples); v > bestV {
			best, bestV = id, v
		}
	}
	return best
}

// ---------- Instruments ----------

//go:embed data/tool-combos.json
var dataFS embed.FS

type comboData struct {
	IDs  []string           `json:"ids"`
	Base float64            `json:"base"`
	V    map[string]float64 `json:"v"`
	max  int
}

var combos *comboData

func init() {
	raw, err := dataFS.ReadFile("data/tool-combos.json")
	if err != nil {
		return
	}
	var f struct {
		Single comboData `json:"single"`
	}
	if json.Unmarshal(raw, &f) == nil && len(f.Single.IDs) > 0 {
		for k := range f.Single.V {
			f.Single.max = max(f.Single.max, len(k))
		}
		combos = &f.Single
	}
}

// toolGains: what each offered instrument adds to the held ones, in Rainbows
// per run, from the measured sets. ok is false without data.
func toolGains(st *engine.State, options []engine.Tool) (map[engine.Tool]float64, bool) {
	d := combos
	if d == nil {
		return nil, false
	}
	idx := map[string]int{}
	for i, id := range d.IDs {
		idx[id] = i
	}
	for _, o := range options {
		if _, ok := idx[engine.ToolIDs[o]]; !ok {
			return nil, false
		}
	}
	key := func(ids []string) string {
		b := make([]byte, len(ids))
		for i, id := range ids {
			b[i] = byte('a' + idx[id])
		}
		sort.Slice(b, func(i, j int) bool { return b[i] < b[j] })
		return string(b)
	}
	value := func(ids []string) (float64, bool) {
		if len(ids) == 0 {
			return d.Base, true
		}
		v, ok := d.V[key(ids)]
		return v, ok
	}
	var held []string
	for t := engine.Tool(0); t < engine.ToolCount; t++ {
		if st.Has(t) {
			if _, ok := idx[engine.ToolIDs[t]]; ok {
				held = append(held, engine.ToolIDs[t])
			}
		}
	}
	size := min(len(held), d.max-1)
	var subsets [][]string
	var pick func(from int, chosen []string)
	pick = func(from int, chosen []string) {
		if len(chosen) == size {
			subsets = append(subsets, append([]string(nil), chosen...))
			return
		}
		for i := from; i < len(held); i++ {
			pick(i+1, append(chosen, held[i]))
		}
	}
	pick(0, nil)
	gains := map[engine.Tool]float64{}
	for _, o := range options {
		sum, n := 0.0, 0
		for _, sub := range subsets {
			a, ok1 := value(append(append([]string(nil), sub...), engine.ToolIDs[o]))
			b, ok2 := value(sub)
			if ok1 && ok2 {
				sum += a - b
				n++
			}
		}
		if n == 0 {
			return nil, false
		}
		gains[o] = sum / float64(n)
	}
	return gains, true
}

func toolsOf(opts []uint8) []engine.Tool {
	out := make([]engine.Tool, len(opts))
	for i, o := range opts {
		out[i] = engine.Tool(o)
	}
	return out
}

func rerollOdds(st *engine.State, options []engine.Tool) (now, next float64, ok bool) {
	var pool []engine.Tool
	onOffer := func(t engine.Tool) bool {
		for _, o := range options {
			if o == t {
				return true
			}
		}
		return false
	}
	for t := engine.Tool(0); t < engine.ToolCount; t++ {
		if !st.Has(t) && !onOffer(t) {
			pool = append(pool, t)
		}
	}
	if len(pool) < 3 {
		pool = pool[:0]
		for t := engine.Tool(0); t < engine.ToolCount; t++ {
			if !st.Has(t) {
				pool = append(pool, t)
			}
		}
	}
	all := append(append([]engine.Tool(nil), options...), pool...)
	gains, ok := toolGains(st, all)
	if !ok || len(pool) < 3 {
		return 0, 0, false
	}
	sum, n := 0.0, 0
	for i := 0; i < len(pool); i++ {
		for j := i + 1; j < len(pool); j++ {
			for k := j + 1; k < len(pool); k++ {
				sum += math.Max(gains[pool[i]], math.Max(gains[pool[j]], gains[pool[k]]))
				n++
			}
		}
	}
	now = -inf
	for _, o := range options {
		now = math.Max(now, gains[o])
	}
	return now, sum / float64(n), true
}

func pickTool(st *engine.State, P *Weights, options []engine.Tool) engine.Tool {
	if P.ToolSets {
		if gains, ok := toolGains(st, options); ok {
			best := options[0]
			for _, o := range options {
				if gains[o] > gains[best] {
					best = o
				}
			}
			return best
		}
	}
	for _, r := range P.ToolRank {
		for _, o := range options {
			if o == r {
				return r
			}
		}
	}
	return options[0]
}
