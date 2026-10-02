package store

import (
	"testing"
	"time"

	"uniserve/internal/model"
)

func TestIdleTrackingAndOffline(t *testing.T) {
	now := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	s := New(Options{IdleAfter: 10 * time.Minute, OfflineAfter: 20 * time.Second})
	s.now = func() time.Time { return now }

	rep := func(util float64, mem int, procs ...model.Proc) model.Report {
		return model.Report{Node: model.NodeMeta{ID: "n1", Vendor: "NVIDIA"},
			GPUs: []model.GPU{{Index: 0, MemUsedMB: mem, MemTotalMB: 80000, UtilPct: util, Procs: procs}}}
	}
	state := func() string { return s.Nodes()[0].GPUs[0].State }
	p := model.Proc{PID: 1, Name: "python", MemMB: 40000}

	s.Upsert(rep(0, 3000)) // baseline memory only, no process
	if state() != model.StateFree {
		t.Fatalf("want free, got %s", state())
	}
	s.Upsert(rep(60, 40000, p))
	if state() != model.StateActive {
		t.Fatalf("want active, got %s", state())
	}
	s.Upsert(rep(0, 40000, p))
	if state() != model.StateIdle {
		t.Fatalf("want idle, got %s", state())
	}
	now = now.Add(11 * time.Minute)
	s.Upsert(rep(1, 40000, p))
	if state() != model.StateHoarded {
		t.Fatalf("want hoarded, got %s", state())
	}
	s.Upsert(rep(70, 40000, p)) // work resumes: the idle clock resets
	s.Upsert(rep(0, 40000, p))
	if state() != model.StateIdle {
		t.Fatalf("want idle after reset, got %s", state())
	}
	now = now.Add(30 * time.Second)
	if n := s.Nodes()[0]; n.Online || n.GPUs[0].State != model.StateOffline {
		t.Fatalf("want offline, got online=%v state=%s", n.Online, n.GPUs[0].State)
	}
}
