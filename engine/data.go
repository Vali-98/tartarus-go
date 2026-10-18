// Package engine is the Tartarus prison rules, ported from the simulator's
// engine.js. It is allocation-light so a Monte Carlo solver can play millions
// of runs: no DOM, no JSON cloning, fixed-size cell arrays.
package engine

const (
	CellCount  = 16
	Office     = 0
	Supreme    = 4
	Rainbow    = 5
	VaultSlots = 10
	logLimit   = 400
)

type Beast uint8

const (
	Mammoth Beast = iota
	Lion
	Wolf
	Rat
)

var BeastNames = [4]string{"Mammoth", "Lion", "Wolf", "Rat"}
var TierNames = [6]string{"Normal", "Magic", "Rare", "Legendary", "Supreme", "Rainbow"}

// FoodChain[b] is what beast b eats; SecondMeal is the extra meal from
// Params.DoubleEatTier up (-1: none).
var FoodChain = [4]Beast{Lion, Wolf, Rat, Mammoth}
var SecondMeal = [4]int8{int8(Wolf), int8(Rat), -1, -1}

type Tool uint8

const (
	ToolRevenge Tool = iota
	ToolMuzzle
	ToolSaw
	ToolKeys
	ToolRope
	ToolFire
	ToolIronbox
	ToolSkewer
	ToolHook
	ToolPestle
	ToolScales
	ToolTongs
	ToolGuillotine
	ToolAxe
	ToolShackles
	ToolCollar
	ToolForged
	ToolDogtag
	ToolCage
	ToolBlacklist
	ToolShortchain
	ToolMaiden
	ToolWhip
	ToolRavenous
	ToolScapegoat
	ToolStake
	ToolCoin
	ToolCrown
	ToolMaul
	ToolClaws
	ToolTrap
	ToolCount
)

// ToolIDs are the simulator's ids, in the order its engine lists them.
var ToolIDs = [ToolCount]string{"revenge", "muzzle", "saw", "keys", "rope", "fire", "ironbox", "skewer", "hook", "pestle", "scales", "tongs", "guillotine", "axe", "shackles", "collar", "forged", "dogtag", "cage", "blacklist", "shortchain", "maiden", "whip", "ravenous", "scapegoat", "stake", "coin", "crown", "maul", "claws", "trap"}

var ToolNames = [ToolCount]string{
	"Avenger's Casket", "Raging Beast's Agonizing Muzzle", "Rusty Saw", "Lost Ring of Keys", "Hangman's Noose",
	"Searing Branding Iron", "Squirming Iron Box: Colossal Rat", "Skewered Rat Heads: Colossal Rat", "Sorting Hook", "Iron Pestle",
	"Warden's Scales", "Iron Tongs of Lust: Mammoth", "Fanged Guillotine: Alpha Wolf", "Gaoler's Execution Axe", "Long-Chained Shackles",
	"Maddened Beast's Barbed Collar", "Forged Key", "Felon's Dog Tag", "Crowded Beastcage", "Executioner's Blacklist",
	"Short-Chained Shackles", "Severed Iron Maiden", "Barbed Whip", "Ferocious Beast's Ravenous Chains", "Scapegoat's Guillotine",
	"Burning Stake", "Kin's Coin of Fate", "Iron Crown of the Prison King: Berserk Lion", "Elephant-Foot Maul: Mammoth",
	"Filthy Claws: Alpha Wolf", "Blood-Stained Trap: Berserk Lion",
}

func ToolByID(id string) (Tool, bool) {
	for i, s := range ToolIDs {
		if s == id {
			return Tool(i), true
		}
	}
	return 0, false
}

type Gift uint8

const (
	GiftRing Gift = iota
	GiftHorn
	GiftWine
	GiftBrand
	GiftCount
)

var GiftIDs = [GiftCount]string{"ring", "horn", "wine", "brand"}
var GiftNames = [GiftCount]string{"Three-Headed Thumb Ring", "Brass Horn of Strife", "Crimson Vintage", "Brand of Original Sin"}

type Event uint8

const (
	EvNone Event = iota
	EvChain
	EvExecution
	EvTrickster
	EvAppetite
	EvConvict
	EvCrow
	EvFreedom
	EvUsurp1
	EvUsurp2
	EvUsurp3
	EvUsurp4
	EvRatway
	EvHunt
	EvPortion
	EvCount
)

var EventIDs = [EvCount]string{"", "chain", "execution", "trickster", "appetite", "convict", "crow", "freedom", "usurp1", "usurp2", "usurp3", "usurp4", "ratway", "hunt", "portion"}
var EventNames = [EvCount]string{"", "Chain reaction", "Execution", "Behind Closed Doors", "Ravenous Appetite", "High-Risk Convict", "Crow Whispers", "Taste of Freedom", "Usurpation I", "Usurpation II", "Usurpation III", "Usurpation IV", "Sewer Ratway", "\"Treasure\" Hunt", "Extra Portion"}

func EventByID(id string) Event {
	for i, s := range EventIDs {
		if i > 0 && s == id {
			return Event(i)
		}
	}
	return EvNone
}

// initialPool is the game's "Prison Incidents" list at the start of a run.
var initialPool = []Event{EvChain, EvExecution, EvTrickster, EvAppetite, EvConvict, EvCrow, EvFreedom, EvUsurp1, EvRatway, EvHunt, EvPortion}

type usurp struct {
	count int // -1: every inmate
	by    int
	next  Event
}

var usurpOf = map[Event]usurp{
	EvUsurp1: {1, 1, EvUsurp2},
	EvUsurp2: {2, 1, EvUsurp3},
	EvUsurp3: {2, 2, EvUsurp4},
	EvUsurp4: {-1, 2, EvNone},
}

const (
	FreedomCells    = 3
	PortionInmates  = 3
	CrowCells       = 4
	HuntBelow       = 2
	InstigateEvents = 1
	InstigateEmpty  = 2
	AppetiteTries   = 5
	AppetiteOdds    = 0.5
)

type Objective uint8

const (
	ObjKing Objective = iota
	ObjWalk
	ObjConsume
	ObjIncident
	ObjDoors
	ObjOther
)

var ObjectiveIDs = [6]string{"king", "walk", "consume", "incident", "doors", "other"}
var ObjectiveNeed = [6]int{0, 18, 4, 2, 10, 0}

type StartEvent uint8

const (
	StartNone StartEvent = iota
	StartCageFight
	StartIronLaw
	StartTyranny
	StartOpulence
)

var StartIDs = [5]string{"", "cagefight", "ironlaw", "tyranny", "opulence"}

type Phase uint8

const (
	PhasePatrol Phase = iota
	PhaseManage
	PhaseOffice
	PhaseEvent
	PhaseEnded
)

var PhaseIDs = [5]string{"patrol", "manage", "office", "event", "ended"}

// Params are the practice values (the simulator's Lab) with live-measured defaults.
type Params struct {
	DrillDays            int
	Destinations         int
	PatrolRange          int
	EventTwoDoors        bool
	FewerDoorsChance     float64
	SwapsPerDay          int
	ReusableCells        bool
	ChainStop            bool
	TierWeights          [5]float64
	SwapWeights          [5]float64
	BeastWeights         [4]float64
	DoubleEatTier        int
	EventStartChance     float64
	EventMorningChance   float64
	EventWalkChance      float64
	EventActionChance    float64
	ExecutionSameTier    bool
	ExecutionRerolls     bool
	IvoryCount           int
	IvoryIncludesSwapped bool
	PreyRounds           int
	ScratchStacksBase    bool
	NestDays             int
	NestTier             int
	Rewards              bool
}

func DefaultParams() Params {
	return Params{
		DrillDays: 20, Destinations: 3, PatrolRange: 6, SwapsPerDay: 1, ReusableCells: true, ChainStop: true,
		TierWeights: [5]float64{40, 38, 12, 8, 2}, SwapWeights: [5]float64{52, 32, 9, 6, 1.5}, BeastWeights: [4]float64{25, 25, 25, 25},
		DoubleEatTier: 3, EventStartChance: 72, EventMorningChance: 5, EventWalkChance: 9, EventActionChance: 4,
		IvoryCount: 4, IvoryIncludesSwapped: true, PreyRounds: 3, ScratchStacksBase: true, NestDays: 2, NestTier: 2, Rewards: true,
	}
}
