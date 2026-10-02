// Package sim is a built-in fleet simulator. It pushes reports through the
// same Upsert path a real agent uses, so the rest of the system cannot tell
// the difference. It exists so the product can be demonstrated, and its
// scenarios rehearsed, without access to real hardware.
package sim

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"

	"uniserve/internal/model"
	"uniserve/internal/store"
)

type fault struct {
	code string
	raw  string
}

type gpu struct {
	index    int
	memMB    int
	baseUtil float64
	util     float64
	temp     float64
	workload *model.Workload
	procs    []model.Proc
	idle     *time.Time
	fault    *fault
	drained  bool
}

type node struct {
	meta    model.NodeMeta
	totalMB int
	idleW   float64
	maxW    float64
	busBase int
	gpus    []*gpu
}

// Scenario is a fault the operator can inject from the UI.
type Scenario struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	GPU   string `json:"gpu"`
	code  string
	raw   string
}

// Sim owns the simulated nodes.
type Sim struct {
	mu    sync.Mutex
	st    *store.Store
	rng   *rand.Rand
	nodes []*node
}

// New builds the default simulated fleet.
func New(st *store.Store) *Sim {
	s := &Sim{st: st, rng: rand.New(rand.NewSource(time.Now().UnixNano()))}
	s.build()
	return s
}

// Scenarios lists the faults that can be injected.
func (s *Sim) Scenarios() []Scenario {
	return []Scenario{
		{ID: "nvidia-xid79", Label: "NVIDIA: GPU falls off the bus (Xid 79)", GPU: "isb-h200-01:1",
			code: "Xid 79", raw: "NVRM: Xid (PCI:0000:1a:00): 79, pid='<unknown>', name=<unknown>, GPU has fallen off the bus."},
		{ID: "ascend-ecc", Label: "Ascend: uncorrectable HBM memory error", GPU: "khi-ascend-01:4",
			code: "Health: Alarm", raw: "npu-smi info -t health -i 4: Health Status: Alarm · Error: HBM multi-bit ECC error, uncorrectable"},
	}
}

func proc(pid int, name, user string, mem int) []model.Proc {
	return []model.Proc{{PID: pid, Name: name, User: user, MemMB: mem}}
}

func (s *Sim) build() {
	now := time.Now()
	ago := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	wl := func(name, owner, kind, class string) *model.Workload {
		return &model.Workload{Name: name, Owner: owner, Kind: kind, DataClass: class}
	}

	h200 := &node{
		meta: model.NodeMeta{ID: "isb-h200-01", Hostname: "isb-h200-01", Vendor: "NVIDIA", Accelerator: "H200",
			Provider: "On-prem", Site: "Islamabad", Jurisdiction: "PK", CostPerGPUHour: 3.40,
			Source: model.SourceSimulated, RawTool: "nvidia-smi"},
		totalMB: 143771, idleW: 72, maxW: 600, busBase: 0x19,
		gpus: []*gpu{
			{index: 0, memMB: 120832, baseUtil: 78, workload: wl("fraud-scoring-llm", "risk-engineering", "serving", model.ClassRestricted), procs: proc(41822, "vllm serve", "svc-risk", 120832)},
			{index: 1, memMB: 120832, baseUtil: 74, workload: wl("fraud-scoring-llm", "risk-engineering", "serving", model.ClassRestricted), procs: proc(41907, "vllm serve", "svc-risk", 120832)},
			{index: 2, memMB: 98304, baseUtil: 0, idle: ago(76 * time.Hour), workload: wl("jupyter: exploration.ipynb", "data-science", "notebook", model.ClassInternal), procs: proc(30211, "python (ipykernel)", "hamza", 98304)},
			{index: 3, memMB: 72704, baseUtil: 0, idle: ago(76 * time.Hour), workload: wl("jupyter: exploration.ipynb", "data-science", "notebook", model.ClassInternal), procs: proc(30211, "python (ipykernel)", "hamza", 72704)},
		},
	}

	ascend := &node{
		meta: model.NodeMeta{ID: "khi-ascend-01", Hostname: "khi-ascend-01", Vendor: "Huawei", Accelerator: "Ascend 910B3",
			Provider: "National cloud", Site: "Karachi", Jurisdiction: "PK", CostPerGPUHour: 1.50,
			Source: model.SourceSimulated, RawTool: "npu-smi"},
		totalMB: 65536, idleW: 92, maxW: 310, busBase: 0xC1,
	}
	for i := 0; i < 4; i++ {
		ascend.gpus = append(ascend.gpus, &gpu{index: i, memMB: 59392, baseUtil: 62 + float64(i*3),
			workload: wl("qwen3-32b-chat", "ai-platform", "serving", model.ClassInternal), procs: proc(8800+i, "vllm-ascend", "svc-platform", 59392)})
	}
	for i := 4; i < 6; i++ {
		ascend.gpus = append(ascend.gpus, &gpu{index: i, memMB: 61440, baseUtil: 95,
			workload: wl("urdu-asr-finetune", "speech-lab", "training", model.ClassInternal), procs: proc(15520, "python train.py", "ayesha", 61440)})
	}
	ascend.gpus = append(ascend.gpus, &gpu{index: 6}, &gpu{index: 7})

	amd := &node{
		meta: model.NodeMeta{ID: "lhe-mi300-01", Hostname: "lhe-mi300-01", Vendor: "AMD", Accelerator: "Instinct MI300X",
			Provider: "Telco colocation", Site: "Lahore", Jurisdiction: "PK", CostPerGPUHour: 2.60,
			Source: model.SourceSimulated, RawTool: "amd-smi"},
		totalMB: 196288, idleW: 110, maxW: 750, busBase: 0x05,
		gpus: []*gpu{
			{index: 0, memMB: 61440, baseUtil: 41, workload: wl("doc-embedding-svc", "search", "serving", model.ClassInternal), procs: proc(5120, "text-embeddings-router", "svc-search", 61440)},
			{index: 1, memMB: 143360, baseUtil: 88, workload: wl("nightly-eval", "ai-platform", "batch", model.ClassInternal), procs: proc(22140, "python eval.py", "svc-platform", 143360)},
			{index: 2, memMB: 153600, baseUtil: 0, idle: ago(26 * time.Hour), workload: wl("finetune.py (paused in debugger)", "nlp-team", "training", model.ClassInternal), procs: proc(19877, "python finetune.py", "bilal", 153600)},
			{index: 3},
		},
	}

	foreign := &node{
		meta: model.NodeMeta{ID: "fra-cloud-01", Hostname: "fra-cloud-01", Vendor: "NVIDIA", Accelerator: "A100 80GB",
			Provider: "Foreign cloud (rented)", Site: "Frankfurt", Jurisdiction: "DE", CostPerGPUHour: 2.90,
			Source: model.SourceSimulated, RawTool: "nvidia-smi"},
		totalMB: 81920, idleW: 55, maxW: 300, busBase: 0x3B,
		gpus: []*gpu{
			{index: 0, memMB: 45056, baseUtil: 55, workload: wl("kyc-document-ocr", "customer-onboarding", "serving", model.ClassRestricted), procs: proc(3312, "tritonserver", "svc-kyc", 45056)},
			{index: 1, memMB: 22528, baseUtil: 31, workload: wl("marketing-image-gen", "marketing", "serving", model.ClassPublic), procs: proc(9644, "python serve.py", "svc-mkt", 22528)},
		},
	}

	s.nodes = []*node{h200, ascend, amd, foreign}
	for _, n := range s.nodes {
		for _, g := range n.gpus {
			g.util = g.baseUtil
			g.temp = 36 + g.baseUtil*0.4
		}
	}
}

// Run pushes reports until ctx is cancelled.
func (s *Sim) Run(ctx context.Context, every time.Duration) {
	s.Push()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.mu.Lock()
			s.step()
			s.mu.Unlock()
			s.Push()
		}
	}
}

func (s *Sim) step() {
	for _, n := range s.nodes {
		for _, g := range n.gpus {
			target := 34.0
			switch {
			case g.fault != nil:
				g.util = 0
			case g.baseUtil > 0:
				g.util += (g.baseUtil-g.util)*0.3 + s.rng.NormFloat64()*3.2
				g.util = math.Max(1, math.Min(100, g.util))
				target = 36 + g.util*0.4
			default:
				g.util = 0
				if g.workload != nil {
					target = 41
				}
			}
			g.temp += (target-g.temp)*0.25 + s.rng.NormFloat64()*0.4
		}
	}
}

// Push sends the current state of every simulated node to the store.
func (s *Sim) Push() {
	s.mu.Lock()
	reports := make([]model.Report, 0, len(s.nodes))
	for _, n := range s.nodes {
		reports = append(reports, s.report(n))
	}
	s.mu.Unlock()
	for _, r := range reports {
		_ = s.st.Upsert(r)
	}
}

func (s *Sim) report(n *node) model.Report {
	r := model.Report{Node: n.meta, CollectedAt: time.Now()}
	for _, g := range n.gpus {
		out := model.GPU{
			Index:      g.index,
			UUID:       fmt.Sprintf("SIM-%s-%d", n.meta.ID, g.index),
			Model:      n.meta.Accelerator,
			MemUsedMB:  g.memMB,
			MemTotalMB: n.totalMB,
			UtilPct:    math.Round(g.util),
			TempC:      math.Round(g.temp),
			PowerW:     math.Round(n.idleW + (n.maxW-n.idleW)*g.util/100*0.92),
			Health:     model.HealthOK,
			Procs:      append([]model.Proc(nil), g.procs...),
			Drained:    g.drained,
		}
		if g.workload != nil {
			w := *g.workload
			out.Workload = &w
		}
		if g.idle != nil {
			t := *g.idle
			out.IdleSince = &t
		}
		if g.fault != nil {
			out.Health = model.HealthCritical
			out.HealthCode = g.fault.code
			out.HealthRaw = g.fault.raw
			out.PowerW = 0
		}
		r.GPUs = append(r.GPUs, out)
	}
	r.Raw = renderRaw(n, r.GPUs)
	return r
}

// Owns reports whether a node belongs to the simulator.
func (s *Sim) Owns(nodeID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range s.nodes {
		if n.meta.ID == nodeID {
			return true
		}
	}
	return false
}

func (s *Sim) find(gpuID string) (*node, *gpu) {
	for _, n := range s.nodes {
		for _, g := range n.gpus {
			if model.GPUID(n.meta.ID, g.index) == gpuID {
				return n, g
			}
		}
	}
	return nil, nil
}

// InjectFault applies one of the scripted fault scenarios.
func (s *Sim) InjectFault(id string) (Scenario, error) {
	for _, sc := range s.Scenarios() {
		if sc.ID != id {
			continue
		}
		s.mu.Lock()
		_, g := s.find(sc.GPU)
		if g == nil {
			s.mu.Unlock()
			return sc, fmt.Errorf("scenario target %s not found", sc.GPU)
		}
		g.fault = &fault{code: sc.code, raw: sc.raw}
		g.util = 0
		g.idle = nil
		s.mu.Unlock()
		s.Push()
		return sc, nil
	}
	return Scenario{}, fmt.Errorf("unknown scenario %q", id)
}

// Reclaim frees an allocated GPU (stops whatever is holding it).
func (s *Sim) Reclaim(gpuID string) error {
	s.mu.Lock()
	_, g := s.find(gpuID)
	if g == nil {
		s.mu.Unlock()
		return fmt.Errorf("%s is not a simulated GPU", gpuID)
	}
	wipe(g)
	s.mu.Unlock()
	s.Push()
	return nil
}

// Migrate moves the workload on one GPU to another, freeing the target first
// if it is being held idle. A faulted source is left drained.
func (s *Sim) Migrate(fromID, toID string) error {
	s.mu.Lock()
	_, from := s.find(fromID)
	toNode, to := s.find(toID)
	if from == nil || to == nil {
		s.mu.Unlock()
		return fmt.Errorf("migration needs two simulated GPUs (%s -> %s)", fromID, toID)
	}
	if from.workload == nil {
		s.mu.Unlock()
		return fmt.Errorf("%s has no workload to move", fromID)
	}
	if to.fault != nil {
		s.mu.Unlock()
		return fmt.Errorf("%s is faulted and cannot take a workload", toID)
	}
	wipe(to)
	to.workload = from.workload
	to.procs = from.procs
	to.memMB = int(math.Min(float64(from.memMB), float64(toNode.totalMB)*0.95))
	for i := range to.procs {
		to.procs[i].MemMB = to.memMB
	}
	to.baseUtil = from.baseUtil
	if to.baseUtil < 5 {
		to.baseUtil = 60
	}
	to.util = to.baseUtil * 0.4 // warming up
	wipe(from)
	from.drained = from.fault != nil
	s.mu.Unlock()
	s.Push()
	return nil
}

// wipe removes whatever is allocated on a GPU. Faults are hardware state and
// survive it.
func wipe(g *gpu) {
	g.workload = nil
	g.procs = nil
	g.memMB = 0
	g.baseUtil = 0
	g.util = 0
	g.idle = nil
}

// Reset rebuilds the default fleet.
func (s *Sim) Reset() {
	s.mu.Lock()
	old := s.nodes
	s.build()
	s.mu.Unlock()
	for _, n := range old {
		s.st.Remove(n.meta.ID)
	}
	s.st.ClearEvents()
	s.Push()
}
