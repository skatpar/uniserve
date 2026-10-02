// Package agent is the node-side half of UniServe: a read-only loop that
// collects telemetry and pushes it to the control plane over an outbound
// HTTP(S) connection. It opens no ports and executes nothing it is sent.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"uniserve/internal/collect"
	"uniserve/internal/model"
)

// Config is everything an agent needs to know about where it runs.
type Config struct {
	Server       string
	Token        string
	NodeID       string
	Provider     string
	Site         string
	Jurisdiction string
	CostPerHour  float64
	DataClass    string
	Tool         string // auto | nvidia | ascend
	ReplayFile   string
	Interval     time.Duration
	Once         bool
}

// Run collects and pushes until ctx is cancelled.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Server == "" {
		return fmt.Errorf("--server is required, e.g. http://10.0.0.5:8080")
	}
	if cfg.NodeID == "" {
		cfg.NodeID, _ = os.Hostname()
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Second
	}
	tool := cfg.Tool
	if cfg.ReplayFile == "" && (tool == "" || tool == "auto") {
		t, err := collect.Detect()
		if err != nil {
			return err
		}
		tool = t
	}
	url := strings.TrimRight(cfg.Server, "/") + "/api/v1/ingest"
	client := &http.Client{Timeout: 10 * time.Second}
	log.Printf("uniserve agent: node=%s -> %s every %s", cfg.NodeID, url, cfg.Interval)

	failures := 0
	for {
		rep, err := build(ctx, cfg, tool)
		if err == nil {
			err = push(ctx, client, url, cfg.Token, rep)
		}
		if err != nil {
			failures++
			log.Printf("uniserve agent: %v", err)
			if cfg.Once {
				return err
			}
		} else {
			if failures > 0 || cfg.Once {
				log.Printf("uniserve agent: reported %d accelerators", len(rep.GPUs))
			}
			failures = 0
			if cfg.Once {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(cfg.Interval):
		}
	}
}

func build(ctx context.Context, cfg Config, tool string) (model.Report, error) {
	var res collect.Result
	var err error
	source := model.SourceLive
	if cfg.ReplayFile != "" {
		res, err = collect.Replay(cfg.ReplayFile)
		source = model.SourceReplay
	} else {
		res, err = collect.Live(ctx, tool)
	}
	if err != nil {
		return model.Report{}, err
	}
	class := cfg.DataClass
	if class == "" {
		class = model.ClassUnlabelled
	}
	// Without a scheduler to ask, the best description of a workload is the
	// process holding the most memory and the user who started it.
	for i := range res.GPUs {
		g := &res.GPUs[i]
		if len(g.Procs) == 0 {
			continue
		}
		top := g.Procs[0]
		for _, p := range g.Procs[1:] {
			if p.MemMB > top.MemMB {
				top = p
			}
		}
		owner := top.User
		if owner == "" {
			owner = "unknown"
		}
		g.Workload = &model.Workload{Name: fmt.Sprintf("%s (pid %d)", filepath.Base(top.Name), top.PID), Owner: owner, Kind: "process", DataClass: class}
	}
	return model.Report{
		Node: model.NodeMeta{
			ID: cfg.NodeID, Hostname: cfg.NodeID, Vendor: res.Vendor, Accelerator: res.Accelerator,
			Provider: cfg.Provider, Site: cfg.Site, Jurisdiction: strings.ToUpper(cfg.Jurisdiction),
			CostPerGPUHour: cfg.CostPerHour, Source: source, RawTool: res.Tool,
		},
		GPUs:        res.GPUs,
		Raw:         res.Raw,
		CollectedAt: time.Now(),
	}, nil
}

func push(ctx context.Context, client *http.Client, url, token string, rep model.Report) error {
	body, err := json.Marshal(rep)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("control plane answered %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}
