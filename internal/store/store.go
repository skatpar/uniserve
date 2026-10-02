// Package store is the in-memory control-plane state: the latest report from
// every node, idle tracking, the activity log and a short utilization history.
package store

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"uniserve/internal/model"
)

const (
	maxEvents  = 60
	maxHistory = 150
	// With no process list to go on, a GPU counts as allocated once it holds
	// this share of its memory. Accelerators report a few GB of baseline use
	// even when empty, so the bar sits above that.
	allocatedFraction = 0.10
	// Below this utilization a GPU is considered quiet.
	quietUtilPct = 5.0
)

// Options tune how raw telemetry is interpreted.
type Options struct {
	IdleAfter    time.Duration // allocated-but-quiet for this long => hoarded
	OfflineAfter time.Duration // no report for this long => offline
}

// Store holds fleet state. It is safe for concurrent use.
type Store struct {
	mu        sync.RWMutex
	opts      Options
	nodes     map[string]*model.Node
	idleSince map[string]time.Time // GPU id -> when it was first seen allocated and quiet
	health    map[string]string    // GPU id -> last seen health, to detect transitions
	events    []model.Event
	history   []float64
	now       func() time.Time
}

// New creates an empty store.
func New(opts Options) *Store {
	if opts.IdleAfter <= 0 {
		opts.IdleAfter = 10 * time.Minute
	}
	if opts.OfflineAfter <= 0 {
		opts.OfflineAfter = 20 * time.Second
	}
	return &Store{
		opts:      opts,
		nodes:     map[string]*model.Node{},
		idleSince: map[string]time.Time{},
		health:    map[string]string{},
		now:       time.Now,
	}
}

// Upsert records a report from an agent or the simulator.
func (s *Store) Upsert(r model.Report) error {
	if r.Node.ID == "" {
		return fmt.Errorf("report has no node id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()

	n, known := s.nodes[r.Node.ID]
	if !known {
		n = &model.Node{}
		s.nodes[r.Node.ID] = n
		s.addEventLocked(model.Event{Time: now, Kind: "node",
			Text: fmt.Sprintf("Node %s joined: %d × %s %s (%s, %s)", r.Node.ID, len(r.GPUs), r.Node.Vendor, r.Node.Accelerator, r.Node.Site, r.Node.Jurisdiction)})
	} else if !n.Online {
		s.addEventLocked(model.Event{Time: now, Kind: "node", Text: fmt.Sprintf("Node %s is reporting again", r.Node.ID)})
	}
	n.NodeMeta = r.Node
	if n.Hostname == "" {
		n.Hostname = n.ID
	}
	n.LastSeen = now
	n.Online = true
	n.Raw = r.Raw
	n.GPUs = make([]model.GPU, len(r.GPUs))

	for i, g := range r.GPUs {
		g.ID = model.GPUID(n.ID, g.Index)
		if g.Health == "" {
			g.Health = model.HealthOK
		}
		if g.Model == "" {
			g.Model = n.Accelerator
		}
		s.trackIdleLocked(&g, now)
		g.State = s.deriveState(g, now)

		if prev := s.health[g.ID]; prev != g.Health {
			if g.Health == model.HealthCritical {
				s.addEventLocked(model.Event{Time: now, Kind: "fault",
					Text: fmt.Sprintf("Fault on %s GPU %d: %s", n.ID, g.Index, firstNonEmpty(g.HealthCode, g.HealthRaw, "critical health"))})
			} else if prev == model.HealthCritical {
				s.addEventLocked(model.Event{Time: now, Kind: "info",
					Text: fmt.Sprintf("%s GPU %d is healthy again", n.ID, g.Index)})
			}
			s.health[g.ID] = g.Health
		}
		n.GPUs[i] = g
	}
	return nil
}

// trackIdleLocked maintains IdleSince for GPUs that don't carry their own.
func (s *Store) trackIdleLocked(g *model.GPU, now time.Time) {
	allocated := isAllocated(*g)
	quiet := g.UtilPct < quietUtilPct
	if !allocated || !quiet || g.Health == model.HealthCritical {
		delete(s.idleSince, g.ID)
		g.IdleSince = nil
		return
	}
	if g.IdleSince != nil { // the reporter knows better (simulator, replay)
		s.idleSince[g.ID] = *g.IdleSince
		return
	}
	since, ok := s.idleSince[g.ID]
	if !ok {
		since = now
		s.idleSince[g.ID] = since
	}
	g.IdleSince = &since
}

func (s *Store) deriveState(g model.GPU, now time.Time) string {
	switch {
	case g.Health == model.HealthCritical:
		return model.StateFaulted
	case !isAllocated(g):
		return model.StateFree
	case g.UtilPct >= quietUtilPct:
		return model.StateActive
	case g.IdleSince != nil && now.Sub(*g.IdleSince) >= s.opts.IdleAfter:
		return model.StateHoarded
	default:
		return model.StateIdle
	}
}

func isAllocated(g model.GPU) bool {
	if g.Workload != nil || len(g.Procs) > 0 {
		return true
	}
	if g.MemTotalMB <= 0 {
		return false
	}
	return float64(g.MemUsedMB) >= allocatedFraction*float64(g.MemTotalMB)
}

// Nodes returns a deep copy of the fleet, sorted for stable display, with
// offline nodes marked.
func (s *Store) Nodes() []model.Node {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	out := make([]model.Node, 0, len(s.nodes))
	for _, n := range s.nodes {
		if n.Online && now.Sub(n.LastSeen) > s.opts.OfflineAfter {
			n.Online = false
			s.addEventLocked(model.Event{Time: now, Kind: "node", Text: fmt.Sprintf("Node %s stopped reporting", n.ID)})
		}
		c := *n
		c.GPUs = make([]model.GPU, len(n.GPUs))
		for i, g := range n.GPUs {
			if !n.Online {
				g.State = model.StateOffline
			} else {
				g.State = s.deriveState(g, now)
			}
			if g.Workload != nil {
				w := *g.Workload
				g.Workload = &w
			}
			g.Procs = append([]model.Proc(nil), g.Procs...)
			c.GPUs[i] = g
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		// Home-first ordering is the engine's job; here just keep it stable.
		return out[i].ID < out[j].ID
	})
	return out
}

// Remove forgets a node (used when the simulator is reset).
func (s *Store) Remove(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n, ok := s.nodes[nodeID]; ok {
		for _, g := range n.GPUs {
			delete(s.idleSince, g.ID)
			delete(s.health, g.ID)
		}
		delete(s.nodes, nodeID)
	}
}

// AddEvent appends to the activity log.
func (s *Store) AddEvent(e model.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.Time.IsZero() {
		e.Time = s.now()
	}
	s.addEventLocked(e)
}

func (s *Store) addEventLocked(e model.Event) {
	s.events = append(s.events, e)
	if len(s.events) > maxEvents {
		s.events = s.events[len(s.events)-maxEvents:]
	}
}

// Events returns the activity log, newest first.
func (s *Store) Events() []model.Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.Event, len(s.events))
	for i, e := range s.events {
		out[len(s.events)-1-i] = e
	}
	return out
}

// ClearEvents empties the activity log.
func (s *Store) ClearEvents() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = nil
}

// RecordUtil appends one fleet-utilization sample to the rolling history.
func (s *Store) RecordUtil(v float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append(s.history, v)
	if len(s.history) > maxHistory {
		s.history = s.history[len(s.history)-maxHistory:]
	}
}

// History returns the rolling fleet-utilization samples, oldest first.
func (s *Store) History() []float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]float64(nil), s.history...)
}

// IdleAfter exposes the configured idle threshold.
func (s *Store) IdleAfter() time.Duration { return s.opts.IdleAfter }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
