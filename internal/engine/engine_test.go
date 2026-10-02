package engine

import (
	"strings"
	"testing"
	"time"

	"uniserve/internal/model"
)

func fleet(now time.Time) []model.Node {
	idle := now.Add(-50 * time.Hour)
	wl := func(name, class string) *model.Workload {
		return &model.Workload{Name: name, Owner: "team", Kind: "serving", DataClass: class}
	}
	mk := func(id, vendor, j string, cost float64, gpus ...model.GPU) model.Node {
		for i := range gpus {
			gpus[i].Index = i
			gpus[i].ID = model.GPUID(id, i)
			gpus[i].Health = model.HealthOK
		}
		return model.Node{NodeMeta: model.NodeMeta{ID: id, Vendor: vendor, Jurisdiction: j, CostPerGPUHour: cost, Site: "x"}, Online: true, LastSeen: now, GPUs: gpus}
	}
	return []model.Node{
		mk("nv", "NVIDIA", "PK", 4,
			model.GPU{State: model.StateActive, MemUsedMB: 100000, MemTotalMB: 140000, UtilPct: 80, Workload: wl("fraud", model.ClassRestricted)},
			model.GPU{State: model.StateHoarded, MemUsedMB: 90000, MemTotalMB: 140000, IdleSince: &idle, Workload: wl("notebook", model.ClassInternal)}),
		mk("hw", "Huawei", "PK", 1.5,
			model.GPU{State: model.StateFree, MemTotalMB: 65536}),
		mk("abroad", "NVIDIA", "DE", 3,
			model.GPU{State: model.StateActive, MemUsedMB: 40000, MemTotalMB: 81920, UtilPct: 50, Workload: wl("kyc", model.ClassRestricted)}),
	}
}

func TestFindingsAndKPIs(t *testing.T) {
	now := time.Now()
	a := Analyze(fleet(now), Policy{Home: "PK"}, now)

	if a.KPIs.GPUs != 4 || a.KPIs.Hoarded != 1 || a.KPIs.Free != 1 || a.KPIs.ResidencyViolations != 1 {
		t.Fatalf("kpis wrong: %+v", a.KPIs)
	}
	if a.KPIs.WastePerDay != 96 || a.KPIs.InCountryPct != 75 {
		t.Errorf("waste/day=%v in-country=%v", a.KPIs.WastePerDay, a.KPIs.InCountryPct)
	}
	if got := int(a.KPIs.WastedSoFar); got != 200 {
		t.Errorf("wasted so far = %d, want 200 (50h x $4)", got)
	}
	if len(a.Findings) != 2 || a.Findings[0].Type != "residency" || a.Findings[1].Type != "hoarded" {
		t.Fatalf("findings wrong: %+v", a.Findings)
	}
	// The restricted workload abroad must be sent in-country, and to the same
	// vendor even though that means reclaiming an idle-held GPU.
	p := a.Findings[0].Plan
	if p == nil || p.Target != "nv:1" || !p.ReclaimTarget || p.CrossVendor {
		t.Errorf("residency plan wrong: %+v", p)
	}
	if a.Findings[1].IdleFor != "2d 2h" {
		t.Errorf("idle for = %q", a.Findings[1].IdleFor)
	}
}

func TestFaultPlanAndWhatIf(t *testing.T) {
	now := time.Now()
	nodes := fleet(now)
	nodes[0].GPUs[0].State = model.StateFaulted
	nodes[0].GPUs[0].Health = model.HealthCritical
	nodes[0].GPUs[0].HealthCode = "Xid 79"
	a := Analyze(nodes, Policy{Home: "PK"}, now)
	if a.Findings[0].Type != "fault" || a.Findings[0].Plan == nil || a.Findings[0].Plan.Target != "nv:1" {
		t.Fatalf("fault plan wrong: %+v", a.Findings[0])
	}
	// The residency move must not be promised the same GPU; 40 GB fits on the
	// free Ascend card instead.
	if p := a.Findings[1].Plan; p == nil || p.Target != "hw:0" || !p.CrossVendor {
		t.Errorf("second plan should fall back to the Huawei GPU: %+v", p)
	}

	var nv *WhatIf
	a = Analyze(fleet(now), Policy{Home: "PK"}, now)
	for i := range a.WhatIfs {
		if a.WhatIfs[i].ID == "vendor:NVIDIA" {
			nv = &a.WhatIfs[i]
		}
	}
	// Losing NVIDIA: fraud needs 98 GB and only a 64 GB card remains, so it
	// cannot be placed; kyc (39 GB) can.
	if nv == nil || nv.GPUsLost != 3 || len(nv.Placed) != 1 || len(nv.Unplaced) != 1 || !strings.Contains(nv.Verdict, "capacity gap of 98 GB") {
		t.Errorf("what-if wrong: %+v", nv)
	}
}

func TestPlaceRespectsResidency(t *testing.T) {
	now := time.Now()
	nodes := fleet(now)
	nodes[2].GPUs[0].State = model.StateFree
	nodes[2].GPUs[0].Workload = nil
	open := Place(nodes, Policy{Home: "PK"}, 40, false, now)
	restricted := Place(nodes, Policy{Home: "PK"}, 40, true, now)
	if len(open) != 3 || len(restricted) != 2 {
		t.Fatalf("open=%d restricted=%d", len(open), len(restricted))
	}
	if restricted[0].GPU != "hw:0" || restricted[1].GPU != "nv:1" || !restricted[1].NeedsReclaim {
		t.Errorf("ranking wrong: %+v", restricted)
	}
}

func TestHumanDuration(t *testing.T) {
	cases := map[time.Duration]string{76 * time.Hour: "3d 4h", 26 * time.Hour: "26h", 48 * time.Hour: "2d", 90 * time.Second: "1m", 5 * time.Second: "5s"}
	for d, want := range cases {
		if got := HumanDuration(d); got != want {
			t.Errorf("%v: got %q want %q", d, got, want)
		}
	}
}
