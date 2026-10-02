package collect

import (
	"fmt"
	"strconv"
	"strings"

	"uniserve/internal/model"
)

// The exact queries the agent runs. Kept here so the parser and the command
// can never drift apart.
var (
	NvidiaGPUQuery  = []string{"--query-gpu=index,uuid,name,memory.used,memory.total,utilization.gpu,temperature.gpu,power.draw", "--format=csv,noheader,nounits"}
	NvidiaAppsQuery = []string{"--query-compute-apps=gpu_uuid,pid,process_name,used_memory", "--format=csv,noheader,nounits"}
)

// ParseNvidia turns the CSV output of the two nvidia-smi queries above into
// normalized GPUs. gpuCSV is required; appsCSV may be empty.
func ParseNvidia(gpuCSV, appsCSV string) ([]model.GPU, error) {
	var gpus []model.GPU
	byUUID := map[string]int{}
	for _, line := range strings.Split(gpuCSV, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := splitCSV(line)
		if len(f) < 8 {
			return nil, fmt.Errorf("nvidia-smi: expected 8 fields, got %d in %q", len(f), line)
		}
		idx, err := strconv.Atoi(f[0])
		if err != nil {
			return nil, fmt.Errorf("nvidia-smi: bad GPU index in %q", line)
		}
		g := model.GPU{
			Index:  idx,
			UUID:   f[1],
			Model:  strings.TrimPrefix(f[2], "NVIDIA "),
			Health: model.HealthOK,
		}
		// A GPU in trouble reports bracketed errors instead of numbers,
		// e.g. "[Unknown Error]" or "[GPU requires reset]".
		for _, v := range f[3:8] {
			if strings.Contains(v, "Error") || strings.Contains(strings.ToLower(v), "reset") || strings.Contains(v, "ERR") {
				g.Health = model.HealthCritical
				g.HealthCode = strings.Trim(v, "[]")
				g.HealthRaw = "nvidia-smi reports " + v + " for GPU " + f[0]
				break
			}
		}
		g.MemUsedMB = int(num(f[3]))
		g.MemTotalMB = int(num(f[4]))
		g.UtilPct = num(f[5])
		g.TempC = num(f[6])
		g.PowerW = num(f[7])
		byUUID[g.UUID] = len(gpus)
		gpus = append(gpus, g)
	}
	if len(gpus) == 0 {
		return nil, fmt.Errorf("nvidia-smi: no GPUs in output")
	}
	for _, line := range strings.Split(appsCSV, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := splitCSV(line)
		if len(f) < 4 {
			continue
		}
		i, ok := byUUID[f[0]]
		if !ok {
			continue
		}
		pid, _ := strconv.Atoi(f[1])
		gpus[i].Procs = append(gpus[i].Procs, model.Proc{PID: pid, Name: f[2], MemMB: int(num(f[3]))})
	}
	return gpus, nil
}

func splitCSV(line string) []string {
	parts := strings.Split(line, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// num parses a number, treating "[N/A]" and friends as zero.
func num(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v
}
