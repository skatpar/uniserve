package collect

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"uniserve/internal/model"
)

var (
	intRe = regexp.MustCompile(`\d+`)
	busRe = regexp.MustCompile(`^[0-9A-Fa-f]{4}:[0-9A-Fa-f]{2}:[0-9A-Fa-f]{2}\.[0-9A-Fa-f]$`)
)

// ParseAscend turns the table printed by `npu-smi info` into normalized GPUs.
//
// Each NPU is two rows:
//
//	| NPU   Name   | Health  | Power(W)   Temp(C)            Hugepages-Usage(page) |
//	| Chip         | Bus-Id  | AICore(%)  Memory-Usage(MB)   HBM-Usage(MB)         |
//
// followed by a process table. Rows are read by cell, not by column offset,
// so spacing differences between npu-smi versions do not matter.
func ParseAscend(out string) ([]model.GPU, string, error) {
	var gpus []model.GPU
	byNPU := map[int]int{}
	chipName := ""

	type card struct {
		npu    int
		name   string
		health string
		power  float64
		temp   float64
		ok     bool
	}
	var cur card
	inProcs := false

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if len(cells) == 0 || cells[0] == "" {
			continue
		}
		if strings.Contains(line, "Process id") {
			inProcs = true
			continue
		}
		first := strings.Fields(cells[0])
		if len(first) == 0 {
			continue
		}
		id, err := strconv.Atoi(first[0])
		if err != nil {
			continue // header, banner, "No running processes" or an MCU row
		}

		if inProcs {
			if len(cells) < 4 {
				continue
			}
			pid, _ := strconv.Atoi(cells[1])
			mem, _ := strconv.Atoi(cells[3])
			if i, ok := byNPU[id]; ok {
				gpus[i].Procs = append(gpus[i].Procs, model.Proc{PID: pid, Name: cells[2], MemMB: mem})
			}
			continue
		}
		if len(cells) < 3 {
			continue
		}

		if busRe.MatchString(cells[1]) || cells[1] == "NA" {
			// Second row: chip id | bus id | AICore, Memory used/total, HBM used/total
			if !cur.ok {
				continue
			}
			nums := intRe.FindAllString(cells[2], -1)
			if len(nums) < 3 {
				continue
			}
			g := model.GPU{
				Index:  cur.npu,
				UUID:   cells[1],
				Model:  cur.name,
				PowerW: cur.power,
				TempC:  cur.temp,
			}
			g.UtilPct = num(nums[0])
			g.MemUsedMB, g.MemTotalMB = int(num(nums[1])), int(num(nums[2]))
			if len(nums) >= 5 && num(nums[4]) > 0 { // training cards report memory under HBM
				g.MemUsedMB, g.MemTotalMB = int(num(nums[3])), int(num(nums[4]))
			}
			g.Health, g.HealthCode, g.HealthRaw = ascendHealth(cur.health, cur.npu)
			if _, dup := byNPU[cur.npu]; dup {
				continue // multi-chip cards: keep the first chip per NPU id
			}
			byNPU[cur.npu] = len(gpus)
			gpus = append(gpus, g)
			continue
		}

		// First row: npu id + name | health | power, temp, hugepages
		vals := strings.Fields(cells[2])
		if len(vals) < 2 {
			continue
		}
		cur = card{npu: id, name: strings.Join(first[1:], " "), health: cells[1], power: num(vals[0]), temp: num(vals[1]), ok: true}
		if chipName == "" {
			chipName = cur.name
		}
	}
	if len(gpus) == 0 {
		return nil, "", fmt.Errorf("npu-smi: no NPUs found in output")
	}
	return gpus, chipName, nil
}

func ascendHealth(h string, npu int) (level, code, raw string) {
	switch strings.ToLower(h) {
	case "ok":
		return model.HealthOK, "", ""
	case "warning":
		return model.HealthWarning, "Health: Warning", fmt.Sprintf("npu-smi reports Health=Warning for NPU %d", npu)
	default:
		return model.HealthCritical, "Health: " + h, fmt.Sprintf("npu-smi reports Health=%s for NPU %d", h, npu)
	}
}
