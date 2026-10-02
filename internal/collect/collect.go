// Package collect reads accelerator telemetry from vendor tools and turns it
// into the vendor-neutral model. Adding a vendor means adding one parser here;
// nothing downstream changes.
package collect

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"uniserve/internal/model"
)

// Result is one collection from one machine.
type Result struct {
	Vendor      string
	Accelerator string
	Tool        string
	GPUs        []model.GPU
	Raw         string
}

// Detect picks the vendor tool available on this machine.
func Detect() (string, error) {
	if _, err := exec.LookPath("nvidia-smi"); err == nil {
		return "nvidia", nil
	}
	if _, err := exec.LookPath("npu-smi"); err == nil {
		return "ascend", nil
	}
	return "", fmt.Errorf("neither nvidia-smi nor npu-smi found on PATH")
}

// Live runs the vendor tool and parses its output. The agent only ever reads.
func Live(ctx context.Context, tool string) (Result, error) {
	switch tool {
	case "nvidia":
		gpuCSV, err := run(ctx, "nvidia-smi", NvidiaGPUQuery...)
		if err != nil {
			return Result{}, err
		}
		appsCSV, _ := run(ctx, "nvidia-smi", NvidiaAppsQuery...)
		gpus, err := ParseNvidia(gpuCSV, appsCSV)
		if err != nil {
			return Result{}, err
		}
		fillUsers(ctx, gpus)
		raw, _ := run(ctx, "nvidia-smi")
		return Result{Vendor: "NVIDIA", Accelerator: gpus[0].Model, Tool: "nvidia-smi", GPUs: gpus, Raw: raw}, nil
	case "ascend":
		raw, err := run(ctx, "npu-smi", "info")
		if err != nil {
			return Result{}, err
		}
		gpus, chip, err := ParseAscend(raw)
		if err != nil {
			return Result{}, err
		}
		fillUsers(ctx, gpus)
		return Result{Vendor: "Huawei", Accelerator: ascendName(chip), Tool: "npu-smi", GPUs: gpus, Raw: raw}, nil
	}
	return Result{}, fmt.Errorf("unknown tool %q (use nvidia or ascend)", tool)
}

// Replay parses a captured tool output from a file, for demos without live
// access to the hardware. The format is detected from the content: an
// `npu-smi info` table, or the CSV from the two nvidia-smi queries (GPU lines
// first, optional compute-app lines after).
func Replay(path string) (Result, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	text := string(b)
	if strings.Contains(text, "npu-smi") || strings.Contains(text, "AICore") {
		gpus, chip, err := ParseAscend(text)
		if err != nil {
			return Result{}, err
		}
		return Result{Vendor: "Huawei", Accelerator: ascendName(chip), Tool: "npu-smi", GPUs: gpus, Raw: text}, nil
	}
	var gpuLines, appLines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "GPU-") {
			appLines = append(appLines, line)
		} else {
			gpuLines = append(gpuLines, line)
		}
	}
	gpus, err := ParseNvidia(strings.Join(gpuLines, "\n"), strings.Join(appLines, "\n"))
	if err != nil {
		return Result{}, err
	}
	return Result{Vendor: "NVIDIA", Accelerator: gpus[0].Model, Tool: "nvidia-smi", GPUs: gpus, Raw: text}, nil
}

func ascendName(chip string) string {
	if chip == "" {
		return "Ascend"
	}
	if strings.HasPrefix(strings.ToLower(chip), "ascend") {
		return chip
	}
	return "Ascend " + chip
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return string(out), fmt.Errorf("%s: %w", name, err)
	}
	return string(out), nil
}

// fillUsers looks up the owner of each process and tidies its name.
func fillUsers(ctx context.Context, gpus []model.GPU) {
	cache := map[int]string{}
	for i := range gpus {
		for j := range gpus[i].Procs {
			p := &gpus[i].Procs[j]
			p.Name = filepath.Base(p.Name)
			if p.PID <= 0 {
				continue
			}
			u, seen := cache[p.PID]
			if !seen {
				out, err := run(ctx, "ps", "-o", "user=", "-p", strconv.Itoa(p.PID))
				if err == nil {
					u = strings.TrimSpace(out)
				}
				cache[p.PID] = u
			}
			p.User = u
		}
	}
}
