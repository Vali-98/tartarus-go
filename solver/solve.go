package solver

import (
	"context"
	"math"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"tartarus/engine"
)

type Options struct {
	Rollouts int    // playouts per candidate at most
	Workers  int    // goroutines; 0 uses every core but one
	Seed     uint32 // base seed for the dice
	// StopAt ends the check early once the leader is this many paired
	// standard errors ahead of every other option (0: never).
	StopAt float64
}

// Row is one candidate move, ranked by Rainbows vaulted.
type Row struct {
	Label  string  `json:"label"`
	Action Action  `json:"action"`
	Mean   float64 `json:"mean"`
	SE     float64 `json:"se"`
	GapSE  float64 `json:"gapSe"`
	N      int     `json:"n"`
	Best   float64 `json:"best"`
	Policy bool    `json:"policy"`
}

type Summary struct {
	Rounds  int   `json:"rounds"`
	Target  int   `json:"target"`
	Done    bool  `json:"done"`
	Options []Row `json:"options"`
}

// Job is a running Monte Carlo check.
type Job struct {
	cands  []Candidate
	policy []bool
	vals   [][]float64
	bests  [][]float64
	done   []uint32
	next   atomic.Int64
	total  int
	stop   atomic.Bool
	fin    atomic.Bool
	wg     sync.WaitGroup
	opts   Options
}

func sameAction(a, b Action) bool { return a == b }

// Start launches the check on a copy of st and returns at once.
func Start(ctx context.Context, st *engine.State, o Options) *Job {
	if o.Rollouts <= 0 {
		o.Rollouts = 400
	}
	if o.Workers <= 0 {
		o.Workers = max(1, runtime.NumCPU()-1)
	}
	if o.Seed == 0 {
		o.Seed = uint32(time.Now().UnixNano())
	}
	base := st.Clone()
	j := &Job{cands: Candidates(base), opts: o, total: o.Rollouts}
	var pick Action
	if len(j.cands) > 0 {
		pick, _ = decide(base.Clone(), paramsFor(base), &rng32{s: o.Seed ^ 0x5bd1e995})
	}
	n := len(j.cands)
	j.policy = make([]bool, n)
	j.vals = make([][]float64, n)
	j.bests = make([][]float64, n)
	for i := range j.cands {
		j.policy[i] = sameAction(j.cands[i].Action, pick)
		j.vals[i] = make([]float64, o.Rollouts)
		j.bests[i] = make([]float64, o.Rollouts)
	}
	j.done = make([]uint32, o.Rollouts)
	if n <= 1 {
		j.total = 0
		return j
	}
	P := paramsFor(base)
	for w := 0; w < o.Workers; w++ {
		j.wg.Add(1)
		go func() {
			defer j.wg.Done()
			for !j.stop.Load() && ctx.Err() == nil {
				r := int(j.next.Add(1)) - 1
				if r >= o.Rollouts {
					return
				}
				seed := mix(o.Seed, uint32(r))
				for i, c := range j.cands {
					trial := base.Clone()
					hideFuture(trial, seed)
					if !Apply(trial, c.Action).OK {
						j.vals[i][r] = math.NaN()
						continue
					}
					Playout(trial, P, &rng32{s: seed ^ 0x9e3779b9})
					j.vals[i][r] = float64(trial.VaultN)
					best := 0
					for n := range trial.Cells {
						if cl := &trial.Cells[n]; !cl.Sealed && cl.HasInmate && int(cl.Inmate.Tier) > best {
							best = int(cl.Inmate.Tier)
						}
					}
					if trial.VaultN > 0 {
						best = engine.Rainbow
					}
					j.bests[i][r] = float64(best)
				}
				atomic.StoreUint32(&j.done[r], 1)
			}
		}()
	}
	go func() { j.wg.Wait(); j.fin.Store(true) }()
	if o.StopAt > 0 {
		go func() {
			for t := time.NewTicker(40 * time.Millisecond); ; <-t.C {
				if j.finished() || ctx.Err() != nil {
					t.Stop()
					return
				}
				s := j.Snapshot()
				if s.Rounds >= 150 && decisive(s, o.StopAt) {
					j.stop.Store(true)
					t.Stop()
					return
				}
			}
		}()
	}
	return j
}

func mix(a, b uint32) uint32 {
	x := a ^ (b+0x9e3779b9)*0x85ebca6b
	x = (x ^ x>>15) * 0x2c1b3c6d
	x = (x ^ x>>12) * 0x297a2d39
	return x ^ x>>15
}

func decisive(s Summary, z float64) bool {
	if len(s.Options) < 2 {
		return true
	}
	for _, r := range s.Options[1:] {
		if r.GapSE <= 0 || (s.Options[0].Mean-r.Mean)/r.GapSE < z {
			return false
		}
	}
	return true
}

func (j *Job) finished() bool { return j.total == 0 || j.fin.Load() }

// Wait blocks until every worker has stopped.
func (j *Job) Wait() { j.wg.Wait() }

// Stop ends the check; Snapshot still reports what was played.
func (j *Job) Stop() { j.stop.Store(true) }

// Snapshot ranks the candidates over the rounds played so far.
func (j *Job) Snapshot() Summary {
	rounds := make([]int, 0, j.opts.Rollouts)
	for r := range j.done {
		if atomic.LoadUint32(&j.done[r]) == 1 {
			rounds = append(rounds, r)
		}
	}
	type rank struct {
		i        int
		mean, se float64
		n        int
		best     float64
	}
	ranks := make([]rank, len(j.cands))
	for i := range j.cands {
		n, sum, sq, best := 0, 0.0, 0.0, 0.0
		for _, r := range rounds {
			x := j.vals[i][r]
			if math.IsNaN(x) {
				continue
			}
			n++
			sum += x
			sq += x * x
			best += j.bests[i][r]
		}
		rk := rank{i: i, n: n}
		if n > 0 {
			rk.mean = sum / float64(n)
			rk.best = best / float64(n)
		}
		if n > 1 {
			sd := math.Sqrt(math.Max(0, sq/float64(n)-rk.mean*rk.mean))
			rk.se = sd / math.Sqrt(float64(n))
		}
		ranks[i] = rk
	}
	sort.SliceStable(ranks, func(a, b int) bool {
		if ranks[a].mean != ranks[b].mean {
			return ranks[a].mean > ranks[b].mean
		}
		return ranks[a].best > ranks[b].best
	})
	s := Summary{Rounds: len(rounds), Target: j.opts.Rollouts}
	s.Done = j.finished() || len(rounds) >= j.opts.Rollouts || j.stop.Load()
	for k, rk := range ranks {
		gap := 0.0
		if k > 0 {
			gap = pairedSE(j.vals[ranks[0].i], j.vals[rk.i], rounds)
		}
		s.Options = append(s.Options, Row{
			Label: j.cands[rk.i].Label, Action: j.cands[rk.i].Action, Mean: rk.mean, SE: rk.se,
			GapSE: gap, N: rk.n, Best: rk.best, Policy: j.policy[rk.i],
		})
	}
	return s
}

func pairedSE(a, b []float64, rounds []int) float64 {
	n, sum, sq := 0, 0.0, 0.0
	for _, r := range rounds {
		d := a[r] - b[r]
		if math.IsNaN(d) {
			continue
		}
		n++
		sum += d
		sq += d * d
	}
	if n < 2 {
		return math.Inf(1)
	}
	m := sum / float64(n)
	return math.Sqrt(math.Max(0, sq/float64(n)-m*m) / float64(n))
}

// Solve runs a check to completion (or until ctx ends) and returns it.
func Solve(ctx context.Context, st *engine.State, o Options) Summary {
	j := Start(ctx, st, o)
	j.Wait()
	return j.Snapshot()
}
