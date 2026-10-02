// Package engine does the arithmetic: KPIs, findings, action plans and
// what-if analysis over the normalized fleet. It is deterministic on purpose.
// The copilot explains these results in language; it is never asked to do the
// sums itself.
package engine

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"uniserve/internal/model"
)

// Policy is the sovereignty rule set. One rule for now: restricted data stays
// in the home jurisdiction.
type Policy struct {
	Home string `json:"home"`
}

// Plan is a proposed action a human can approve.
type Plan struct {
	Action        string   `json:"action"` // reclaim | migrate
	Source        string   `json:"source"`
	Target        string   `json:"target,omitempty"`
	ReclaimTarget bool     `json:"reclaim_target,omitempty"`
	CrossVendor   bool     `json:"cross_vendor,omitempty"`
	Summary       string   `json:"summary"`
	Notes         []string `json:"notes,omitempty"`
	// Executable is set by the server: true only when every GPU involved is
	// simulated. Agents on real nodes are read-only.
	Executable bool `json:"executable"`
}

// Finding is something the operator should know about, with a plan if there
// is one.
type Finding struct {
	ID          string  `json:"id"`
	Type        string  `json:"type"`     // fault | residency | hoarded | offline
	Severity    string  `json:"severity"` // critical | serious | warning | info
	Title       string  `json:"title"`
	Detail      string  `json:"detail"`
	GPU         string  `json:"gpu,omitempty"`
	Node        string  `json:"node"`
	Workload    string  `json:"workload,omitempty"`
	Owner       string  `json:"owner,omitempty"`
	IdleFor     string  `json:"idle_for,omitempty"`
	WastePerDay float64 `json:"waste_per_day,omitempty"`
	WastedSoFar float64 `json:"wasted_so_far,omitempty"`
	Plan        *Plan   `json:"plan,omitempty"`
}

// VendorShare is one vendor's slice of the fleet.
type VendorShare struct {
	Vendor string  `json:"vendor"`
	GPUs   int     `json:"gpus"`
	MemGB  int     `json:"mem_gb"`
	Pct    float64 `json:"pct"`
}

// KPIs are the headline numbers.
type KPIs struct {
	Nodes               int           `json:"nodes"`
	NodesOnline         int           `json:"nodes_online"`
	GPUs                int           `json:"gpus"`
	ByVendor            []VendorShare `json:"by_vendor"`
	AvgUtil             float64       `json:"avg_util"`
	Active              int           `json:"active"`
	Idle                int           `json:"idle"`
	Hoarded             int           `json:"hoarded"`
	Free                int           `json:"free"`
	Faulted             int           `json:"faulted"`
	Offline             int           `json:"offline"`
	WastePerDay         float64       `json:"waste_per_day"`
	WastedSoFar         float64       `json:"wasted_so_far"`
	FleetCostPerDay     float64       `json:"fleet_cost_per_day"`
	ReclaimableGB       int           `json:"reclaimable_gb"`
	FreeGB              int           `json:"free_gb"`
	InCountryPct        float64       `json:"in_country_pct"`
	ResidencyViolations int           `json:"residency_violations"`
}

// Move is one workload relocation inside a what-if.
type Move struct {
	Workload    string `json:"workload"`
	From        string `json:"from"`
	NeedGB      int    `json:"need_gb"`
	DataClass   string `json:"data_class"`
	To          string `json:"to,omitempty"`
	ToVendor    string `json:"to_vendor,omitempty"`
	ViaReclaim  bool   `json:"via_reclaim,omitempty"`
	CrossVendor bool   `json:"cross_vendor,omitempty"`
}

// WhatIf answers "what happens if this slice of the fleet disappears?".
type WhatIf struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	GPUsLost int    `json:"gpus_lost"`
	Placed   []Move `json:"placed"`
	Unplaced []Move `json:"unplaced"`
	Verdict  string `json:"verdict"`
}

// Candidate is a GPU that could host a new workload.
type Candidate struct {
	GPU          string  `json:"gpu"`
	Vendor       string  `json:"vendor"`
	Accelerator  string  `json:"accelerator"`
	Site         string  `json:"site"`
	Jurisdiction string  `json:"jurisdiction"`
	Provider     string  `json:"provider"`
	TotalGB      int     `json:"total_gb"`
	CostPerHour  float64 `json:"cost_per_hour"`
	NeedsReclaim bool    `json:"needs_reclaim"`
	IdleFor      string  `json:"idle_for,omitempty"`
}

// Analysis is everything the engine derives from one snapshot.
type Analysis struct {
	KPIs     KPIs      `json:"kpis"`
	Findings []Finding `json:"findings"`
	WhatIfs  []WhatIf  `json:"what_ifs"`
}

// Analyze derives KPIs, findings and what-ifs from a fleet snapshot.
func Analyze(nodes []model.Node, pol Policy, now time.Time) Analysis {
	a := Analysis{}
	a.KPIs = kpis(nodes, pol, now)
	a.Findings = findings(nodes, pol, now)
	for _, f := range a.Findings {
		if f.Type == "residency" {
			a.KPIs.ResidencyViolations++
		}
	}
	a.WhatIfs = whatIfs(nodes, pol)
	return a
}

func kpis(nodes []model.Node, pol Policy, now time.Time) KPIs {
	k := KPIs{Nodes: len(nodes)}
	vendors := map[string]*VendorShare{}
	var utilSum float64
	var utilN, home int
	for _, n := range nodes {
		if n.Online {
			k.NodesOnline++
		}
		v := vendors[n.Vendor]
		if v == nil {
			v = &VendorShare{Vendor: n.Vendor}
			vendors[n.Vendor] = v
		}
		for _, g := range n.GPUs {
			k.GPUs++
			v.GPUs++
			v.MemGB += model.MemGB(g.MemTotalMB)
			if n.Jurisdiction == pol.Home {
				home++
			}
			k.FleetCostPerDay += n.CostPerGPUHour * 24
			switch g.State {
			case model.StateActive:
				k.Active++
			case model.StateIdle:
				k.Idle++
			case model.StateHoarded:
				k.Hoarded++
				k.WastePerDay += n.CostPerGPUHour * 24
				k.ReclaimableGB += model.MemGB(g.MemTotalMB)
				if g.IdleSince != nil {
					k.WastedSoFar += n.CostPerGPUHour * now.Sub(*g.IdleSince).Hours()
				}
			case model.StateFree:
				k.Free++
				k.FreeGB += model.MemGB(g.MemTotalMB)
			case model.StateFaulted:
				k.Faulted++
			case model.StateOffline:
				k.Offline++
			}
			if g.State != model.StateOffline && g.State != model.StateFaulted {
				utilSum += g.UtilPct
				utilN++
			}
		}
	}
	if utilN > 0 {
		k.AvgUtil = math.Round(utilSum/float64(utilN)*10) / 10
	}
	if k.GPUs > 0 {
		k.InCountryPct = math.Round(float64(home) / float64(k.GPUs) * 100)
	}
	for _, v := range vendors {
		if k.GPUs > 0 {
			v.Pct = math.Round(float64(v.GPUs) / float64(k.GPUs) * 100)
		}
		k.ByVendor = append(k.ByVendor, *v)
	}
	sort.Slice(k.ByVendor, func(i, j int) bool {
		return vendorRank(k.ByVendor[i].Vendor) < vendorRank(k.ByVendor[j].Vendor)
	})
	return k
}

// vendorRank fixes the display order of vendors so colours never move.
func vendorRank(v string) int {
	switch v {
	case "NVIDIA":
		return 0
	case "Huawei":
		return 1
	case "AMD":
		return 2
	}
	return 3
}

type slot struct {
	node *model.Node
	gpu  *model.GPU
}

// targets lists GPUs that could take a workload: free ones, and ones being
// held idle that could be reclaimed first.
func targets(nodes []model.Node) []slot {
	var out []slot
	for i := range nodes {
		n := &nodes[i]
		if !n.Online {
			continue
		}
		for j := range n.GPUs {
			g := &n.GPUs[j]
			if g.State == model.StateFree || g.State == model.StateHoarded {
				out = append(out, slot{n, g})
			}
		}
	}
	return out
}

// pick chooses the best target for a workload needing needMB. Preference:
// same vendor (no re-porting of the model), then already free over
// needs-reclaim, then in-country, then the tightest fit, then the cheapest.
func pick(cands []slot, needMB int, restricted bool, vendor string, pol Policy, taken map[string]bool, skip func(slot) bool) *slot {
	var best *slot
	var bestKey [5]float64
	for i := range cands {
		c := cands[i]
		if taken[c.gpu.ID] || c.gpu.MemTotalMB < needMB {
			continue
		}
		if restricted && c.node.Jurisdiction != pol.Home {
			continue
		}
		if skip != nil && skip(c) {
			continue
		}
		key := [5]float64{
			b2f(c.node.Vendor != vendor),
			b2f(c.gpu.State != model.StateFree),
			b2f(c.node.Jurisdiction != pol.Home),
			float64(c.gpu.MemTotalMB),
			c.node.CostPerGPUHour,
		}
		if best == nil || less(key, bestKey) {
			best, bestKey = &cands[i], key
		}
	}
	return best
}

func less(a, b [5]float64) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func findings(nodes []model.Node, pol Policy, now time.Time) []Finding {
	var faults, residency, hoarded, offline []Finding
	cands := targets(nodes)
	taken := map[string]bool{}

	migrate := func(n *model.Node, g *model.GPU, restricted bool) *Plan {
		t := pick(cands, g.MemUsedMB, restricted, n.Vendor, pol, taken, nil)
		if t == nil {
			return nil
		}
		taken[t.gpu.ID] = true
		p := &Plan{Action: "migrate", Source: g.ID, Target: t.gpu.ID,
			ReclaimTarget: t.gpu.State == model.StateHoarded, CrossVendor: t.node.Vendor != n.Vendor}
		dest := fmt.Sprintf("%s GPU %d", t.node.ID, t.gpu.Index)
		if p.ReclaimTarget {
			idle := ""
			if t.gpu.IdleSince != nil {
				idle = ", idle " + HumanDuration(now.Sub(*t.gpu.IdleSince))
			}
			p.Summary = fmt.Sprintf("Reclaim %s (held by %s%s) and restart %s there", dest, ownerOf(t.gpu), idle, g.Workload.Name)
		} else {
			p.Summary = fmt.Sprintf("Restart %s on %s (free)", g.Workload.Name, dest)
		}
		p.Notes = append(p.Notes, fmt.Sprintf("Needs %d GB; target has %d GB in %s, %s.", model.MemGB(g.MemUsedMB), model.MemGB(t.gpu.MemTotalMB), t.node.Site, t.node.Jurisdiction))
		if p.CrossVendor {
			p.Notes = append(p.Notes, fmt.Sprintf("Target is %s, source is %s: the model needs a build for the target runtime.", t.node.Vendor, n.Vendor))
		}
		return p
	}

	for i := range nodes {
		n := &nodes[i]
		if !n.Online {
			offline = append(offline, Finding{ID: "offline:" + n.ID, Type: "offline", Severity: "serious", Node: n.ID,
				Title:  fmt.Sprintf("%s has stopped reporting", n.ID),
				Detail: fmt.Sprintf("%d × %s at %s (%s). Last report %s ago.", len(n.GPUs), n.Accelerator, n.Site, n.Provider, HumanDuration(now.Sub(n.LastSeen)))})
			continue
		}
		for j := range n.GPUs {
			g := &n.GPUs[j]
			switch {
			case g.State == model.StateFaulted:
				f := Finding{ID: "fault:" + g.ID, Type: "fault", Severity: "critical", GPU: g.ID, Node: n.ID,
					Title: fmt.Sprintf("%s on %s GPU %d", firstNonEmpty(g.HealthCode, "Hardware fault"), n.ID, g.Index)}
				if g.Workload != nil {
					f.Workload, f.Owner = g.Workload.Name, g.Workload.Owner
					f.Detail = fmt.Sprintf("%s (%s, %s data) is down on this GPU.", g.Workload.Name, g.Workload.Owner, g.Workload.DataClass)
					f.Plan = migrate(n, g, g.Workload.DataClass == model.ClassRestricted)
					if f.Plan == nil {
						f.Detail += " No GPU in the fleet can take it right now."
					}
				} else {
					f.Severity = "warning"
					f.Detail = "No workload on this GPU. Keep it out of service until it is reset or replaced."
					if g.Drained {
						f.Title = fmt.Sprintf("%s GPU %d drained, awaiting repair", n.ID, g.Index)
						f.Detail = fmt.Sprintf("Workload moved off after %s. Keep out of service until reset or replaced.", firstNonEmpty(g.HealthCode, "the fault"))
					}
				}
				faults = append(faults, f)

			case g.Workload != nil && g.Workload.DataClass == model.ClassRestricted && pol.Home != "" && n.Jurisdiction != pol.Home:
				f := Finding{ID: "residency:" + g.ID, Type: "residency", Severity: "serious", GPU: g.ID, Node: n.ID,
					Workload: g.Workload.Name, Owner: g.Workload.Owner,
					Title:  fmt.Sprintf("Restricted data outside %s: %s", pol.Home, g.Workload.Name),
					Detail: fmt.Sprintf("Runs on %s GPU %d in %s, %s (%s). Policy keeps restricted data in %s.", n.ID, g.Index, n.Site, n.Jurisdiction, n.Provider, pol.Home)}
				f.Plan = migrate(n, g, true)
				if f.Plan == nil {
					f.Detail += " No in-country GPU can take it right now."
				}
				residency = append(residency, f)

			case g.State == model.StateHoarded:
				f := Finding{ID: "hoarded:" + g.ID, Type: "hoarded", Severity: "warning", GPU: g.ID, Node: n.ID,
					WastePerDay: n.CostPerGPUHour * 24}
				idle := time.Duration(0)
				if g.IdleSince != nil {
					idle = now.Sub(*g.IdleSince)
				}
				f.IdleFor = HumanDuration(idle)
				f.WastedSoFar = n.CostPerGPUHour * idle.Hours()
				f.Owner = ownerOf(g)
				if g.Workload != nil {
					f.Workload = g.Workload.Name
				}
				f.Title = fmt.Sprintf("%s GPU %d held idle for %s", n.ID, g.Index, f.IdleFor)
				f.Detail = fmt.Sprintf("%s holds %d of %d GB at %.0f%% utilization (%s).", f.Owner, model.MemGB(g.MemUsedMB), model.MemGB(g.MemTotalMB), g.UtilPct, firstNonEmpty(f.Workload, "unknown process"))
				p := &Plan{Action: "reclaim", Source: g.ID,
					Summary: fmt.Sprintf("Notify %s, then release the GPU", f.Owner)}
				if len(g.Procs) > 0 {
					p.Notes = append(p.Notes, fmt.Sprintf("Holding process: %s (pid %d).", g.Procs[0].Name, g.Procs[0].PID))
				}
				f.Plan = p
				hoarded = append(hoarded, f)
			}
		}
	}
	sort.SliceStable(hoarded, func(i, j int) bool { return hoarded[i].WastedSoFar > hoarded[j].WastedSoFar })
	out := append(faults, residency...)
	out = append(out, offline...)
	return append(out, hoarded...)
}

func ownerOf(g *model.GPU) string {
	if g.Workload != nil && g.Workload.Owner != "" {
		return g.Workload.Owner
	}
	for _, p := range g.Procs {
		if p.User != "" {
			return p.User
		}
	}
	return "unknown owner"
}

func whatIfs(nodes []model.Node, pol Policy) []WhatIf {
	var out []WhatIf
	seen := map[string]bool{}
	for _, n := range nodes {
		if seen[n.Vendor] || n.Vendor == "" {
			continue
		}
		seen[n.Vendor] = true
		v := n.Vendor
		out = append(out, whatIf("vendor:"+v, fmt.Sprintf("All %s accelerators become unavailable", v), nodes, pol,
			func(n *model.Node) bool { return n.Vendor == v }))
	}
	sort.Slice(out, func(i, j int) bool {
		return vendorRank(strings.TrimPrefix(out[i].ID, "vendor:")) < vendorRank(strings.TrimPrefix(out[j].ID, "vendor:"))
	})
	foreign := false
	for _, n := range nodes {
		if pol.Home != "" && n.Jurisdiction != pol.Home {
			foreign = true
		}
	}
	if foreign {
		out = append(out, whatIf("foreign", fmt.Sprintf("All capacity outside %s becomes unavailable", pol.Home), nodes, pol,
			func(n *model.Node) bool { return n.Jurisdiction != pol.Home }))
	}
	return out
}

func whatIf(id, label string, nodes []model.Node, pol Policy, lost func(*model.Node) bool) WhatIf {
	w := WhatIf{ID: id, Label: label}
	type demand struct {
		n *model.Node
		g *model.GPU
	}
	var ds []demand
	for i := range nodes {
		n := &nodes[i]
		if !lost(n) {
			continue
		}
		for j := range n.GPUs {
			g := &n.GPUs[j]
			w.GPUsLost++
			// Only work that is actually running needs a new home.
			if g.Workload != nil && (g.State == model.StateActive || g.State == model.StateIdle) {
				ds = append(ds, demand{n, g})
			}
		}
	}
	sort.SliceStable(ds, func(i, j int) bool { return ds[i].g.MemUsedMB > ds[j].g.MemUsedMB })
	cands := targets(nodes)
	taken := map[string]bool{}
	for _, d := range ds {
		m := Move{Workload: d.g.Workload.Name, From: d.g.ID, NeedGB: model.MemGB(d.g.MemUsedMB), DataClass: d.g.Workload.DataClass}
		t := pick(cands, d.g.MemUsedMB, d.g.Workload.DataClass == model.ClassRestricted, d.n.Vendor, pol, taken,
			func(s slot) bool { return lost(s.node) })
		if t == nil {
			w.Unplaced = append(w.Unplaced, m)
			continue
		}
		taken[t.gpu.ID] = true
		m.To, m.ToVendor = t.gpu.ID, t.node.Vendor
		m.ViaReclaim = t.gpu.State == model.StateHoarded
		m.CrossVendor = t.node.Vendor != d.n.Vendor
		w.Placed = append(w.Placed, m)
	}
	switch {
	case len(ds) == 0:
		w.Verdict = "Nothing is running there, so nothing needs to move."
	case len(w.Unplaced) == 0:
		reclaims := 0
		for _, m := range w.Placed {
			if m.ViaReclaim {
				reclaims++
			}
		}
		w.Verdict = fmt.Sprintf("All %d running workloads fit on the remaining fleet", len(ds))
		if reclaims > 0 {
			w.Verdict += fmt.Sprintf(", provided %s reclaimed first", plural(reclaims, "idle-held GPU is", "idle-held GPUs are"))
		}
		w.Verdict += "."
	default:
		gap := 0
		for _, m := range w.Unplaced {
			gap += m.NeedGB
		}
		w.Verdict = fmt.Sprintf("%d of %d running workloads cannot be placed: a capacity gap of %d GB.", len(w.Unplaced), len(ds), gap)
	}
	return w
}

// Place ranks GPUs that could host a new workload of needGB.
func Place(nodes []model.Node, pol Policy, needGB int, restricted bool, now time.Time) []Candidate {
	var out []Candidate
	for _, s := range targets(nodes) {
		if model.MemGB(s.gpu.MemTotalMB) < needGB {
			continue
		}
		if restricted && s.node.Jurisdiction != pol.Home {
			continue
		}
		c := Candidate{GPU: s.gpu.ID, Vendor: s.node.Vendor, Accelerator: s.node.Accelerator, Site: s.node.Site,
			Jurisdiction: s.node.Jurisdiction, Provider: s.node.Provider, TotalGB: model.MemGB(s.gpu.MemTotalMB),
			CostPerHour: s.node.CostPerGPUHour, NeedsReclaim: s.gpu.State == model.StateHoarded}
		if c.NeedsReclaim && s.gpu.IdleSince != nil {
			c.IdleFor = HumanDuration(now.Sub(*s.gpu.IdleSince))
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.NeedsReclaim != b.NeedsReclaim {
			return !a.NeedsReclaim
		}
		if (a.Jurisdiction == pol.Home) != (b.Jurisdiction == pol.Home) {
			return a.Jurisdiction == pol.Home
		}
		if a.TotalGB != b.TotalGB {
			return a.TotalGB < b.TotalGB
		}
		return a.CostPerHour < b.CostPerHour
	})
	return out
}

// HumanDuration renders a duration the way people say it: "3d 4h", "26h", "12m".
func HumanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d >= 48*time.Hour:
		days := int(d.Hours()) / 24
		hours := int(d.Hours()) % 24
		if hours == 0 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd %dh", days, hours)
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
