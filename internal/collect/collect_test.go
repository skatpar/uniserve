package collect

import (
	"os"
	"testing"

	"uniserve/internal/model"
)

func TestParseAscend(t *testing.T) {
	b, err := os.ReadFile("testdata/npu-smi-910b.txt")
	if err != nil {
		t.Fatal(err)
	}
	gpus, chip, err := ParseAscend(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if chip != "910B3" || len(gpus) != 3 {
		t.Fatalf("chip=%q gpus=%d", chip, len(gpus))
	}
	g := gpus[0]
	if g.Index != 0 || g.UtilPct != 71 || g.MemUsedMB != 60512 || g.MemTotalMB != 65536 || g.TempC != 52 || g.PowerW != 187.4 {
		t.Errorf("NPU 0 parsed wrong: %+v", g)
	}
	if g.UUID != "0000:C1:00.0" || g.Health != model.HealthOK {
		t.Errorf("NPU 0 bus/health wrong: %+v", g)
	}
	if len(g.Procs) != 1 || g.Procs[0].PID != 1203456 || g.Procs[0].Name != "python" || g.Procs[0].MemMB != 57200 {
		t.Errorf("NPU 0 procs wrong: %+v", g.Procs)
	}
	if len(gpus[1].Procs) != 0 || gpus[1].MemUsedMB != 3396 {
		t.Errorf("NPU 1 parsed wrong: %+v", gpus[1])
	}
	if gpus[2].Health != model.HealthCritical || gpus[2].HealthCode != "Health: Alarm" {
		t.Errorf("NPU 2 health wrong: %+v", gpus[2])
	}
}

func TestReplayNvidia(t *testing.T) {
	res, err := Replay("testdata/nvidia-smi-h200.csv")
	if err != nil {
		t.Fatal(err)
	}
	if res.Vendor != "NVIDIA" || res.Accelerator != "H200" || len(res.GPUs) != 3 {
		t.Fatalf("unexpected result: %+v", res)
	}
	g := res.GPUs[0]
	if g.MemUsedMB != 120832 || g.MemTotalMB != 143771 || g.UtilPct != 82 || g.TempC != 67 || g.PowerW != 421.33 {
		t.Errorf("GPU 0 parsed wrong: %+v", g)
	}
	if len(g.Procs) != 1 || g.Procs[0].PID != 41822 {
		t.Errorf("GPU 0 procs wrong: %+v", g.Procs)
	}
	if res.GPUs[2].Health != model.HealthCritical || res.GPUs[2].HealthCode != "Unknown Error" {
		t.Errorf("GPU 2 should be critical: %+v", res.GPUs[2])
	}
}

func TestReplayAscendDetected(t *testing.T) {
	res, err := Replay("testdata/npu-smi-910b.txt")
	if err != nil {
		t.Fatal(err)
	}
	if res.Vendor != "Huawei" || res.Accelerator != "Ascend 910B3" || res.Tool != "npu-smi" {
		t.Fatalf("unexpected result: %+v", res)
	}
}
