// Package model defines the vendor-neutral schema every part of UniServe speaks.
// Collectors translate vendor tools (nvidia-smi, npu-smi, ...) into these types;
// everything downstream (engine, copilot, UI) only ever sees this shape.
package model

import (
	"fmt"
	"time"
)

// GPU states derived by the server from normalized telemetry.
const (
	StateActive  = "active"  // allocated and doing work
	StateIdle    = "idle"    // allocated, quiet, but not yet past the idle threshold
	StateHoarded = "hoarded" // allocated, quiet for longer than the idle threshold
	StateFree    = "free"    // nothing allocated
	StateFaulted = "faulted" // vendor tool reports a critical health problem
	StateOffline = "offline" // the node has stopped reporting
)

// Health levels, normalized across vendors.
const (
	HealthOK       = "ok"
	HealthWarning  = "warning"
	HealthCritical = "critical"
)

// Data classes used by the residency policy.
const (
	ClassRestricted = "restricted" // must stay inside the home jurisdiction
	ClassInternal   = "internal"
	ClassPublic     = "public"
	ClassUnlabelled = "unlabelled"
)

// Sources of a node's telemetry.
const (
	SourceLive      = "live"
	SourceSimulated = "simulated"
	SourceReplay    = "replay"
)

// Proc is one process holding accelerator memory.
type Proc struct {
	PID   int    `json:"pid"`
	Name  string `json:"name"`
	User  string `json:"user,omitempty"`
	MemMB int    `json:"mem_mb"`
}

// Workload is what a GPU is being used for, as far as we know.
type Workload struct {
	Name      string `json:"name"`
	Owner     string `json:"owner"`
	Kind      string `json:"kind"` // serving | training | notebook | batch | process
	DataClass string `json:"data_class"`
}

// GPU is one accelerator (GPU or NPU) in vendor-neutral form.
type GPU struct {
	Index      int       `json:"index"`
	UUID       string    `json:"uuid,omitempty"`
	Model      string    `json:"model"`
	MemUsedMB  int       `json:"mem_used_mb"`
	MemTotalMB int       `json:"mem_total_mb"`
	UtilPct    float64   `json:"util_pct"`
	TempC      float64   `json:"temp_c"`
	PowerW     float64   `json:"power_w"`
	Health     string    `json:"health"`
	HealthCode string    `json:"health_code,omitempty"` // vendor's own code, e.g. "Xid 79"
	HealthRaw  string    `json:"health_raw,omitempty"`  // vendor's own wording
	Procs      []Proc    `json:"procs,omitempty"`
	Workload   *Workload `json:"workload,omitempty"`

	// Derived by the server.
	ID        string     `json:"id"` // "<node>:<index>"
	State     string     `json:"state"`
	IdleSince *time.Time `json:"idle_since,omitempty"`
	Drained   bool       `json:"drained,omitempty"`
}

// NodeMeta is the static description an agent sends with every report.
type NodeMeta struct {
	ID             string  `json:"id"`
	Hostname       string  `json:"hostname"`
	Vendor         string  `json:"vendor"`      // NVIDIA | Huawei | AMD
	Accelerator    string  `json:"accelerator"` // e.g. "H200", "Ascend 910B3"
	Provider       string  `json:"provider"`    // who operates the hardware
	Site           string  `json:"site"`        // city / facility
	Jurisdiction   string  `json:"jurisdiction"`
	CostPerGPUHour float64 `json:"cost_per_gpu_hour"`
	Source         string  `json:"source"`
	RawTool        string  `json:"raw_tool"` // the vendor tool the raw text came from
}

// Report is what an agent (or the simulator) pushes to the control plane.
type Report struct {
	Node        NodeMeta  `json:"node"`
	GPUs        []GPU     `json:"gpus"`
	Raw         string    `json:"raw,omitempty"` // unmodified vendor tool output
	CollectedAt time.Time `json:"collected_at"`
}

// Node is a server with accelerators, as held by the control plane.
type Node struct {
	NodeMeta
	LastSeen time.Time `json:"last_seen"`
	Online   bool      `json:"online"`
	GPUs     []GPU     `json:"gpus"`
	Raw      string    `json:"raw,omitempty"`
}

// Event is one line in the activity log.
type Event struct {
	Time  time.Time `json:"time"`
	Kind  string    `json:"kind"` // fault | action | node | info
	Text  string    `json:"text"`
	Actor string    `json:"actor,omitempty"`
}

// GPUID builds the canonical GPU identifier.
func GPUID(node string, index int) string { return fmt.Sprintf("%s:%d", node, index) }

// MemGB renders megabytes as whole gigabytes.
func MemGB(mb int) int { return (mb + 512) / 1024 }
