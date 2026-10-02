package sim

import (
	"fmt"
	"strings"

	"uniserve/internal/model"
)

// renderRaw produces text shaped like the vendor's own tool, so the UI can
// show what an operator would see without UniServe. For simulated nodes this
// is generated, not captured.
func renderRaw(n *node, gpus []model.GPU) string {
	switch n.meta.RawTool {
	case "npu-smi":
		return renderNPUSMI(n, gpus)
	case "amd-smi":
		return renderAMDSMI(n, gpus)
	default:
		return renderNvidiaSMI(n, gpus)
	}
}

func renderNvidiaSMI(n *node, gpus []model.GPU) string {
	var b strings.Builder
	b.WriteString("+-----------------------------------------------------------------------------------------+\n")
	b.WriteString("| NVIDIA-SMI 570.86.15              Driver Version: 570.86.15      CUDA Version: 12.8     |\n")
	b.WriteString("|-----------------------------------------+------------------------+----------------------+\n")
	b.WriteString("| GPU  Name                 Persistence-M | Bus-Id          Disp.A | Volatile Uncorr. ECC |\n")
	b.WriteString("| Fan  Temp   Perf          Pwr:Usage/Cap |           Memory-Usage | GPU-Util  Compute M. |\n")
	b.WriteString("|=========================================+========================+======================|\n")
	var faults []string
	for _, g := range gpus {
		bus := fmt.Sprintf("00000000:%02X:00.0", n.busBase+g.Index)
		name := "NVIDIA " + n.meta.Accelerator
		if g.Health == model.HealthCritical {
			fmt.Fprintf(&b, "| %3d  %-26s On  |   %s Off |                 ERR! |\n", g.Index, name, bus)
			fmt.Fprintf(&b, "| ERR!  ERR!  ERR!           ERR! / %4.0fW |  Unknown Error         |     ERR!      Default |\n", n.maxW)
			faults = append(faults, g.HealthRaw)
		} else {
			fmt.Fprintf(&b, "| %3d  %-26s On  |   %s Off |                    0 |\n", g.Index, name, bus)
			fmt.Fprintf(&b, "| N/A  %3.0fC    P0          %4.0fW / %4.0fW | %7dMiB / %6dMiB |    %3.0f%%      Default |\n",
				g.TempC, g.PowerW, n.maxW, g.MemUsedMB, g.MemTotalMB, g.UtilPct)
		}
		b.WriteString("+-----------------------------------------+------------------------+----------------------+\n")
	}
	b.WriteString("\n+-----------------------------------------------------------------------------------------+\n")
	b.WriteString("| Processes:                                                                              |\n")
	b.WriteString("|  GPU   GI   CI        PID   Type   Process name                              GPU Memory |\n")
	b.WriteString("|=========================================================================================|\n")
	any := false
	for _, g := range gpus {
		for _, p := range g.Procs {
			any = true
			fmt.Fprintf(&b, "|  %3d   N/A  N/A   %7d      C   %-38s %9dMiB |\n", g.Index, p.PID, p.Name, p.MemMB)
		}
	}
	if !any {
		b.WriteString("|  No running processes found                                                             |\n")
	}
	b.WriteString("+-----------------------------------------------------------------------------------------+\n")
	for _, f := range faults {
		b.WriteString("\n$ dmesg | grep -i xid\n" + f + "\n")
	}
	return b.String()
}

func renderNPUSMI(n *node, gpus []model.GPU) string {
	chip := strings.TrimPrefix(n.meta.Accelerator, "Ascend ")
	var b strings.Builder
	b.WriteString("+------------------------------------------------------------------------------------------------+\n")
	b.WriteString("| npu-smi 24.1.0                   Version: 24.1.0                                               |\n")
	b.WriteString("+---------------------------+---------------+----------------------------------------------------+\n")
	b.WriteString("| NPU   Name                | Health        | Power(W)    Temp(C)           Hugepages-Usage(page)|\n")
	b.WriteString("| Chip                      | Bus-Id        | AICore(%)   Memory-Usage(MB)  HBM-Usage(MB)        |\n")
	b.WriteString("+===========================+===============+====================================================+\n")
	var faults []string
	for _, g := range gpus {
		health := "OK"
		if g.Health == model.HealthCritical {
			health = "Alarm"
			faults = append(faults, g.HealthRaw)
		} else if g.Health == model.HealthWarning {
			health = "Warning"
		}
		bus := fmt.Sprintf("0000:%02X:00.0", n.busBase+g.Index)
		fmt.Fprintf(&b, "| %-5d %-19s | %-13s | %-11.1f %-17.0f %-5d/ %-13d |\n", g.Index, chip, health, g.PowerW, g.TempC, 0, 0)
		fmt.Fprintf(&b, "| %-25d | %-13s | %-11.0f %-5d/ %-10d %-6d/ %-12d |\n", 0, bus, g.UtilPct, 0, 0, g.MemUsedMB, g.MemTotalMB)
		b.WriteString("+===========================+===============+====================================================+\n")
	}
	b.WriteString("+---------------------------+---------------+----------------------------------------------------+\n")
	b.WriteString("| NPU     Chip              | Process id    | Process name             | Process memory(MB)      |\n")
	b.WriteString("+===========================+===============+====================================================+\n")
	for _, g := range gpus {
		if len(g.Procs) == 0 {
			fmt.Fprintf(&b, "| %-94s |\n", fmt.Sprintf("No running processes found in NPU %d", g.Index))
		}
		for _, p := range g.Procs {
			fmt.Fprintf(&b, "| %-7d %-17d | %-13d | %-24s | %-23d |\n", g.Index, 0, p.PID, p.Name, p.MemMB)
		}
		b.WriteString("+===========================+===============+====================================================+\n")
	}
	for _, f := range faults {
		b.WriteString("\n$ " + f + "\n")
	}
	return b.String()
}

func renderAMDSMI(n *node, gpus []model.GPU) string {
	var b strings.Builder
	b.WriteString("+------------------------------------------------------------------------------+\n")
	b.WriteString("| AMD-SMI 25.3.0+ede62f2      amdgpu version: 6.12.12   ROCm version: 6.4.0    |\n")
	b.WriteString("|-------------------------------------+----------------------------------------|\n")
	b.WriteString("| BDF                        GPU-Name | Mem-Uti   Temp   UEC       Power-Usage |\n")
	b.WriteString("| GPU  HIP-ID  OAM-ID  Partition-Mode | GFX-Uti    Fan               Mem-Usage |\n")
	b.WriteString("|=====================================+========================================|\n")
	for _, g := range gpus {
		bus := fmt.Sprintf("0000:%02x:00.0", n.busBase+g.Index*8)
		memPct := 0
		if g.MemTotalMB > 0 {
			memPct = g.MemUsedMB * 100 / g.MemTotalMB
		}
		fmt.Fprintf(&b, "| %s  AMD %-15s | %3d %%   %3.0f °C   0      %4.0f/%4.0f W |\n", bus, n.meta.Accelerator, memPct, g.TempC, g.PowerW, n.maxW)
		fmt.Fprintf(&b, "| %3d  %6d  %6d             SPX | %3.0f %%    N/A       %6d/%6d MB |\n", g.Index, g.Index, g.Index, g.UtilPct, g.MemUsedMB, g.MemTotalMB)
		b.WriteString("|-------------------------------------+----------------------------------------|\n")
	}
	b.WriteString("+------------------------------------------------------------------------------+\n")
	b.WriteString("| Processes:                                                                   |\n")
	b.WriteString("|  GPU        PID  Process Name          GTT_MEM  CPU_MEM  VRAM_MEM  MEM_USAGE |\n")
	b.WriteString("|==============================================================================|\n")
	any := false
	for _, g := range gpus {
		for _, p := range g.Procs {
			any = true
			fmt.Fprintf(&b, "|  %3d  %9d  %-20s   0.0 B    0.0 B  %5d MB   %5d MB |\n", g.Index, p.PID, truncate(p.Name, 20), p.MemMB, p.MemMB)
		}
	}
	if !any {
		b.WriteString("|  No running processes found                                                  |\n")
	}
	b.WriteString("+------------------------------------------------------------------------------+\n")
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
