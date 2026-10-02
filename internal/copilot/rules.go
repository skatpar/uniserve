package copilot

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"uniserve/internal/engine"
	"uniserve/internal/model"
)

// The rules engine is the fallback when no model is available. It covers the
// handful of questions the product is built around and is honest about being
// rules: the UI labels every answer with its source.

var gbRe = regexp.MustCompile(`(\d+)\s*gb`)

func rulesAnswer(question string, s Snapshot, note string) *Answer {
	text := rules(question, s)
	return &Answer{Source: "rules", Note: note, stream: func(emit func(string)) error {
		emit(text)
		return nil
	}}
}

func has(q string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(q, w) {
			return true
		}
	}
	return false
}

func rules(question string, s Snapshot) string {
	q := strings.ToLower(question)
	switch {
	case has(q, "what if", "what breaks", "unavailable", "lose ", "lost", "export control", "sanction", "depend"):
		return ruleWhatIf(q, s)
	case has(q, "where", "place", "deploy", "host") && has(q, "run", "model", "workload", "deploy", "gb"):
		return rulePlacement(q, s)
	case has(q, "fault", "error", "xid", "alarm", "incident", "broken", "ecc", "went wrong", "is wrong"):
		return ruleFault(s)
	case has(q, "buy", "idle", "waste", "hoard", "more gpu", "capacity", "reclaim", "save"):
		return ruleIdle(s)
	case has(q, "residency", "policy", "complian", "sovereign", "restricted", "abroad", "outside"):
		return ruleResidency(s)
	default:
		return ruleBriefing(s)
	}
}

func ruleIdle(s Snapshot) string {
	k := s.Analysis.KPIs
	var b strings.Builder
	if k.Hoarded == 0 {
		fmt.Fprintf(&b, "Nothing is being held idle right now. **%d of %d GPUs are free** (%d GB) and average utilization is **%.0f%%**.", k.Free, k.GPUs, k.FreeGB, k.AvgUtil)
		if k.Free == 0 {
			b.WriteString(" With no free and no idle-held GPUs, new work does need new capacity.")
		}
		return b.String()
	}
	fmt.Fprintf(&b, "**No, reclaim before you buy.** %s allocated but doing nothing, which costs **$%.0f a day** ($%.0f so far). Releasing them frees **%d GB**, on top of the %d GB already free.\n\n",
		plural(k.Hoarded, "GPU is", "GPUs are"), k.WastePerDay, k.WastedSoFar, k.ReclaimableGB, k.FreeGB)
	for _, f := range s.Analysis.Findings {
		if f.Type != "hoarded" {
			continue
		}
		fmt.Fprintf(&b, "- **%s**: held by %s for %s (%s), $%.0f/day\n", gpuLabel(f.GPU), f.Owner, f.IdleFor, f.Workload, f.WastePerDay)
	}
	fmt.Fprintf(&b, "\nFleet-wide average utilization is %.0f%%.", k.AvgUtil)
	return b.String()
}

func ruleFault(s Snapshot) string {
	var b strings.Builder
	n := 0
	for _, f := range s.Analysis.Findings {
		if f.Type != "fault" {
			continue
		}
		g, node := findGPU(s, f.GPU)
		if g == nil {
			continue
		}
		n++
		meaning, next := explainCode(g.HealthCode, g.HealthRaw)
		fmt.Fprintf(&b, "**%s: %s.** %s\n\n", gpuLabel(f.GPU), firstNonEmpty(g.HealthCode, "hardware fault"), meaning)
		if g.HealthRaw != "" {
			fmt.Fprintf(&b, "- Vendor message: `%s`\n", g.HealthRaw)
		}
		if f.Workload != "" {
			fmt.Fprintf(&b, "- Affected: **%s** (%s), on %s %s in %s\n", f.Workload, f.Owner, node.Vendor, node.Accelerator, node.Site)
		} else {
			b.WriteString("- Affected: no workload is on this GPU now\n")
		}
		if f.Plan != nil {
			fmt.Fprintf(&b, "- Proposed: %s. %s\n", f.Plan.Summary, strings.Join(f.Plan.Notes, " "))
		}
		fmt.Fprintf(&b, "- Hardware: %s\n\n", next)
	}
	if n == 0 {
		return "No faults right now. Every reporting GPU is healthy."
	}
	return strings.TrimSpace(b.String())
}

// explainCode translates the vendor codes this build knows into plain language.
func explainCode(code, raw string) (meaning, next string) {
	c := strings.ToLower(code + " " + raw)
	switch {
	case strings.Contains(c, "xid 79"):
		return "The GPU stopped responding on the PCIe bus, so the driver can no longer talk to it. Usual causes are a hardware, power or thermal problem, not the application.",
			"drain the GPU, check power and seating, reboot the node; if it recurs, replace the card."
	case strings.Contains(c, "xid 48"), strings.Contains(c, "xid 94"), strings.Contains(c, "xid 95"):
		return "The GPU hit an uncorrectable memory (ECC) error. Results from this GPU cannot be trusted until it is reset.",
			"drain and reset the GPU; repeated errors mean the card should be replaced."
	case strings.Contains(c, "xid 63"), strings.Contains(c, "xid 64"):
		return "The GPU is retiring bad memory cells. It still works but its memory is degrading.",
			"schedule a reset at the next quiet window and watch for repeats."
	case strings.Contains(c, "xid 13"), strings.Contains(c, "xid 31"), strings.Contains(c, "xid 43"):
		return "The fault came from the application or driver rather than the hardware.",
			"restart the workload; if it repeats on other GPUs the bug is in the job, not the card."
	case strings.Contains(c, "ecc"):
		return "The accelerator's on-board memory reported an error it could not correct. Anything computed on it since is suspect.",
			"take the NPU out of service, reset it, and raise it with the vendor if the alarm returns."
	case strings.Contains(c, "alarm"), strings.Contains(c, "critical"):
		return "The vendor tool reports the accelerator as unhealthy.",
			"take it out of service and run the vendor's health diagnostics."
	case strings.Contains(c, "unknown error"), strings.Contains(c, "reset"):
		return "The driver cannot read this GPU's state.",
			"drain it and reset the GPU or reboot the node."
	}
	return "The vendor tool reports a critical health problem.", "take it out of service and check the vendor diagnostics."
}

func ruleWhatIf(q string, s Snapshot) string {
	want := ""
	switch {
	case has(q, "nvidia"):
		want = "vendor:NVIDIA"
	case has(q, "huawei", "ascend"):
		want = "vendor:Huawei"
	case has(q, "amd"):
		want = "vendor:AMD"
	case has(q, "foreign", "outside", "abroad", "cloud"):
		want = "foreign"
	}
	var b strings.Builder
	for _, w := range s.Analysis.WhatIfs {
		if want != "" && w.ID != want {
			continue
		}
		fmt.Fprintf(&b, "**%s** (%d GPUs lost). %s\n\n", w.Label, w.GPUsLost, w.Verdict)
		for _, m := range w.Placed {
			fmt.Fprintf(&b, "- %s (%d GB, %s) moves to **%s**%s%s\n", m.Workload, m.NeedGB, m.DataClass, gpuLabel(m.To),
				tern(m.ViaReclaim, " after reclaiming it", ""), tern(m.CrossVendor, "; needs a build for "+m.ToVendor, ""))
		}
		for _, m := range w.Unplaced {
			fmt.Fprintf(&b, "- %s (%d GB, %s): **no capacity**\n", m.Workload, m.NeedGB, m.DataClass)
		}
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		return "No matching scenario. Ask about NVIDIA, Huawei, AMD, or capacity outside the home jurisdiction."
	}
	return strings.TrimSpace(b.String())
}

func rulePlacement(q string, s Snapshot) string {
	need := 24
	if m := gbRe.FindStringSubmatch(q); m != nil {
		need, _ = strconv.Atoi(m[1])
	}
	restricted := has(q, "restricted", "bank", "sensitive", "pii", "in-country", "in country", "confidential", "customer data")
	cands := engine.Place(s.Nodes, s.Policy, need, restricted, s.Now)
	var b strings.Builder
	scope := "anywhere in the fleet"
	if restricted {
		scope = "inside " + s.Policy.Home + " only, because the data is restricted"
	}
	if len(cands) == 0 {
		return fmt.Sprintf("No GPU can take a **%d GB** workload %s right now. Nothing free or idle-held is large enough.", need, scope)
	}
	top := cands[0]
	fmt.Fprintf(&b, "**Use %s** (%s %s, %s, %s). It has %d GB, costs $%.2f/hour%s.\n\nLooked for %d GB %s. Ranked options:\n\n",
		gpuLabel(top.GPU), top.Vendor, top.Accelerator, top.Site, top.Jurisdiction, top.TotalGB, top.CostPerHour,
		tern(top.NeedsReclaim, " and must be reclaimed first (idle "+top.IdleFor+")", " and is free now"), need, scope)
	for i, c := range cands {
		if i == 5 {
			break
		}
		fmt.Fprintf(&b, "- %s: %s %s, %d GB, %s, $%.2f/h, %s\n", gpuLabel(c.GPU), c.Vendor, c.Accelerator, c.TotalGB, c.Jurisdiction, c.CostPerHour,
			tern(c.NeedsReclaim, "reclaim first (idle "+c.IdleFor+")", "free"))
	}
	if restricted {
		skipped := 0
		for _, n := range s.Nodes {
			if n.Jurisdiction != s.Policy.Home {
				skipped += len(n.GPUs)
			}
		}
		if skipped > 0 {
			fmt.Fprintf(&b, "\n%d GPUs outside %s were excluded by the residency policy.", skipped, s.Policy.Home)
		}
	}
	return b.String()
}

func ruleResidency(s Snapshot) string {
	k := s.Analysis.KPIs
	var b strings.Builder
	if k.ResidencyViolations == 0 {
		fmt.Fprintf(&b, "No residency violations. **%.0f%% of GPUs are in %s** and every restricted workload runs there.", k.InCountryPct, s.Policy.Home)
		return b.String()
	}
	fmt.Fprintf(&b, "**%s running outside %s.** %.0f%% of GPUs are in-country.\n\n", plural(k.ResidencyViolations, "restricted workload is", "restricted workloads are"), s.Policy.Home, k.InCountryPct)
	for _, f := range s.Analysis.Findings {
		if f.Type != "residency" {
			continue
		}
		fmt.Fprintf(&b, "- **%s** (%s): %s", f.Workload, f.Owner, f.Detail)
		if f.Plan != nil {
			fmt.Fprintf(&b, " Proposed: %s.", f.Plan.Summary)
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func ruleBriefing(s Snapshot) string {
	k := s.Analysis.KPIs
	var b strings.Builder
	fmt.Fprintf(&b, "**%d GPUs on %d nodes, %.0f%% average utilization.** %d working, %d held idle, %d free, %d faulted.\n\n", k.GPUs, k.Nodes, k.AvgUtil, k.Active+k.Idle, k.Hoarded, k.Free, k.Faulted)
	for _, v := range k.ByVendor {
		fmt.Fprintf(&b, "- %s: %d GPUs (%.0f%%)\n", v.Vendor, v.GPUs, v.Pct)
	}
	fmt.Fprintf(&b, "- Idle-held waste: $%.0f/day\n- In %s: %.0f%% of GPUs; residency violations: %d\n", k.WastePerDay, s.Policy.Home, k.InCountryPct, k.ResidencyViolations)
	if len(s.Analysis.Findings) > 0 {
		fmt.Fprintf(&b, "\nMost urgent: %s.", s.Analysis.Findings[0].Title)
	}
	return b.String()
}

func findGPU(s Snapshot, id string) (*model.GPU, *model.Node) {
	for i := range s.Nodes {
		for j := range s.Nodes[i].GPUs {
			if s.Nodes[i].GPUs[j].ID == id {
				return &s.Nodes[i].GPUs[j], &s.Nodes[i]
			}
		}
	}
	return nil, nil
}

// gpuLabel turns "node:3" into "node GPU 3".
func gpuLabel(id string) string {
	if i := strings.LastIndex(id, ":"); i >= 0 {
		return id[:i] + " GPU " + id[i+1:]
	}
	return id
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
