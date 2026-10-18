package engine

import "strings"

// NewOpts configures a simulated run (the solver's playouts and tests).
type NewOpts struct {
	Params     *Params
	StartEvent StartEvent
	StartTools int // instrument picks offered at the start (the live game gives 1)
}

func mix32(x uint32) uint32 {
	x += 0x9e3779b9
	x = (x ^ x>>16) * 0x85ebca6b
	x = (x ^ x>>13) * 0xc2b2ae35
	return x ^ x>>16
}

func newBlank(p Params) *State {
	s := &State{Params: p, Day: 1, MaxDays: p.DrillDays, NextID: 1, Phase: PhasePatrol}
	for n := range s.Cells {
		s.Cells[n] = blankCell(n)
	}
	copy(s.Pool[:], initialPool)
	s.PoolN = len(initialPool)
	return s
}

// NewGame starts a run from an empty prison, as the simulator's drill does.
func NewGame(seed uint32, o NewOpts) *State {
	p := DefaultParams()
	if o.Params != nil {
		p = *o.Params
	}
	s := newBlank(p)
	s.Rng = mix32(seed)
	s.StartEvent = o.StartEvent
	if o.StartEvent == StartIronLaw {
		s.MaxDays = 30
	}
	side := State{Rng: mix32(seed ^ 0x51ed270b)}
	s.Objective = [2]Objective{ObjKing, ObjWalk}[int(side.rand()*2)]
	s.spawnEvent(p.EventStartChance)
	picks := o.StartTools
	for i := 0; i < picks; i++ {
		pool := make([]int, ToolCount)
		for t := range pool {
			pool[t] = t
		}
		pool = side.shuffle(pool)[:3]
		opts := make([]uint8, 3)
		for k, t := range pool {
			opts[k] = uint8(t)
		}
		s.Pending = append(s.Pending, Choice{Options: opts})
	}
	s.beginDay()
	return s
}

// ---------- Boards typed or read from the live game ----------

type CellInput struct {
	Kind    string `json:"kind"`  // sealed, open, empty, nest
	Beast   string `json:"beast"` // mammoth, lion, wolf, rat
	Tier    int    `json:"tier"`  // 0 Normal .. 4 Supreme
	Event   string `json:"event"` // an event id, "unknown" (kind not visible), or ""
	Known   int    `json:"known"` // sealed doors: 1 rarity shown (tier), 2 beast and rarity shown
	AtLeast int    `json:"atLeast"`
	Scratch bool   `json:"scratch"`
	Ivory   bool   `json:"ivory"`
	NestDue int    `json:"nestDue"`
}

type BoardInput struct {
	Cells           [CellCount]CellInput `json:"cells"`
	Warden          int                  `json:"warden"`
	Day             int                  `json:"day"`
	MaxDays         int                  `json:"maxDays"`
	Phase           string               `json:"phase"` // patrol, manage, office, event
	Dest            []int                `json:"dest"`
	Tools           []string             `json:"tools"`
	Vault           int                  `json:"vault"`
	Objective       string               `json:"objective"`
	Progress        int                  `json:"progress"`
	Milestone       bool                 `json:"milestone"`
	StartEvent      string               `json:"startEvent"`
	Chain           bool                 `json:"chain"`
	ChainSelect     bool                 `json:"chainSelect"`
	ChainTargets    []int                `json:"chainTargets"`
	UsedSwap        bool                 `json:"usedSwap"`
	UsedDevour      bool                 `json:"usedDevour"`
	NoSwap          bool                 `json:"noSwap"`
	NoDevour        bool                 `json:"noDevour"`
	DevourAfterSwap bool                 `json:"devourAfterSwap"`
	EventKind       string               `json:"eventKind"` // phase event: the event waiting
	Pending         string               `json:"pending"`   // "", "tool" or "gift"
	Offer           []string             `json:"offer"`     // ids on offer for the pending choice
	GiftPicks       int                  `json:"giftPicks"`
	Rerolls         int                  `json:"rerolls"`
	FreshOffer      bool                 `json:"freshOffer"`
}

func beastByID(id string) Beast {
	for i, n := range BeastNames {
		if strings.EqualFold(n, id) {
			return Beast(i)
		}
	}
	return Mammoth
}

// FromBoard builds a state to solve from what the player sees. Anything the
// board doesn't show stays hidden for the solver to re-roll.
func FromBoard(b BoardInput) (*State, string) {
	p := DefaultParams()
	s := newBlank(p)
	s.Day = clamp(b.Day, 1, 99)
	s.MaxDays = b.MaxDays
	for i, id := range StartIDs {
		if i > 0 && id == b.StartEvent {
			s.StartEvent = StartEvent(i)
		}
	}
	if s.MaxDays <= 0 {
		s.MaxDays = p.DrillDays
		if s.StartEvent == StartIronLaw {
			s.MaxDays = 30
		}
	}
	if s.MaxDays < s.Day {
		s.MaxDays = s.Day
	}
	s.Rng = mix32(uint32(s.Day)*7919 + 17)
	s.Position = clamp(b.Warden, 0, CellCount-1)
	s.VaultN = clamp(b.Vault, 0, VaultSlots)
	s.Objective = ObjOther
	for i, id := range ObjectiveIDs {
		if id == b.Objective {
			s.Objective = Objective(i)
		}
	}
	s.Progress, s.Milestone = b.Progress, b.Milestone
	for _, id := range b.Tools {
		if t, ok := ToolByID(id); ok {
			s.AddTool(t)
		}
	}
	for n := 1; n < CellCount; n++ {
		in, c := b.Cells[n], &s.Cells[n]
		*c = blankCell(n)
		tier := clamp(in.Tier, 0, Supreme)
		switch in.Kind {
		case "open":
			c.Sealed, c.HasInmate = false, true
			c.Inmate = s.makeInmate(beastByID(in.Beast), tier)
			c.Ivory = in.Ivory
			if int(c.Inmate.Tier) > s.Stats.Best {
				s.Stats.Best = int(c.Inmate.Tier)
			}
		case "empty":
			c.Sealed = false
		case "nest":
			c.Nest, c.NestDue = true, int16(in.NestDue)
			if in.NestDue <= 0 {
				c.NestDue = int16(s.Day + p.NestDays)
			}
		default:
			c.AtLeast = int8(clamp(in.AtLeast, 0, Supreme))
			if in.Known >= 1 {
				c.HasHidden, c.Known = true, int8(clamp(in.Known, 1, 2))
				c.Hidden = s.makeInmate(beastByID(in.Beast), tier)
			}
			c.Scratch = in.Scratch
			if c.Scratch && !c.HasHidden {
				c.HasHidden, c.Known = true, 1
				c.Hidden = s.makeInmate(Beast(int(s.Rng>>3)%4), tier)
			}
		}
		if in.Event == "unknown" {
			c.Event, c.EventUnknown, c.Seen = EventPool0(s), true, true
		} else if e := EventByID(in.Event); e != EvNone {
			c.Event, c.Seen = e, true
		}
	}
	s.Cells[Office] = blankCell(Office)
	s.Cells[Office].Sealed = false
	switch b.Phase {
	case "choice":
		if !b.FreshOffer {
			s.Phase = PhaseManage
			break
		}
		s.Phase = PhasePatrol
		s.spawnEvent(p.EventStartChance)
		s.beginDay()
	case "manage":
		s.Phase = PhaseManage
	case "office":
		s.Phase = PhaseOffice
	case "event":
		if e := EventByID(b.EventKind); e != EvNone {
			s.Phase = PhaseEvent
			s.Event = EventState{Active: true, Kind: e, Cell: s.Position}
		} else {
			return nil, "Pick the event that is waiting."
		}
	default:
		s.Phase = PhasePatrol
		for _, n := range b.Dest {
			if n >= 0 && n < CellCount && s.DestN < CellCount {
				s.Dest[s.DestN] = int8(n)
				s.DestN++
			}
		}
		if s.DestN == 0 {
			return nil, "Enter today's door cards (the cells you can walk to)."
		}
	}
	s.Chain = b.Chain
	if b.UsedSwap {
		s.Used.Swap = 1
	}
	if b.UsedDevour {
		s.Used.Devour = 1
	}
	s.Used.NoSwap = b.NoSwap
	s.Used.NoDevour = b.NoDevour
	s.Used.DevourAfterSwap = b.DevourAfterSwap
	if b.ChainSelect {
		if !s.onBoard(s.Position) {
			return nil, "The inmate in the warden's cell wasn't read for this consume selection."
		}
		s.Chaining = Chaining{Active: true, Eater: s.Cells[s.Position].Inmate.ID, Bites: 1, OffersSet: true}
		for _, n := range b.ChainTargets {
			if n >= 0 && n < CellCount {
				s.Chaining.Offers[n] = true
			}
		}
		found := false
		for _, offer := range s.Chaining.Offers {
			found = found || offer
		}
		if !found {
			return nil, "No consume targets were read from the screenshot."
		}
	}
	if b.Pending != "" && len(b.Offer) > 0 {
		ch := Choice{Gift: b.Pending == "gift", Rerolls: b.Rerolls}
		for _, id := range b.Offer {
			if ch.Gift {
				for g, gid := range GiftIDs {
					if gid == id {
						ch.Options = append(ch.Options, uint8(g))
					}
				}
			} else if t, ok := ToolByID(id); ok {
				ch.Options = append(ch.Options, uint8(t))
			}
		}
		if len(ch.Options) > 0 {
			s.Pending = append(s.Pending, ch)
			if ch.Gift {
				picks := clamp(b.GiftPicks, 1, 30)
				for i := 1; i < picks; i++ {
					s.Pending = append(s.Pending, Choice{Gift: true, Options: append([]uint8(nil), ch.Options...)})
				}
			}
		}
	}
	return s, ""
}

// EventPool0 is a placeholder kind for an event whose kind isn't visible; the
// solver re-rolls it per playout (hideFuture).
func EventPool0(s *State) Event { return s.Pool[0] }
