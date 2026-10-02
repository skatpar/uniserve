// Package copilot answers operator questions about the fleet. It sends the
// normalized snapshot and the engine's findings to an OpenAI-compatible
// endpoint that the operator controls (vLLM, LM Studio, Ollama, ...). If no
// model is configured or reachable it falls back to a small rules engine and
// says so, so the answer is never passed off as model output.
package copilot

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"uniserve/internal/engine"
	"uniserve/internal/model"
)

// Config points the copilot at a model endpoint.
type Config struct {
	BaseURL  string `json:"base_url"` // e.g. http://10.0.0.7:8000/v1
	Model    string `json:"model"`
	APIKey   string `json:"api_key,omitempty"`
	HostNode string `json:"host_node,omitempty"` // fleet node that serves the model, if any
}

// Status is what the UI shows about the copilot.
type Status struct {
	Enabled  bool   `json:"enabled"`
	Model    string `json:"model,omitempty"`
	Endpoint string `json:"endpoint,omitempty"` // host:port only
	BaseURL  string `json:"base_url,omitempty"`
	Locality string `json:"locality,omitempty"` // local | private | external | unknown
	HostNode string `json:"host_node,omitempty"`
	HasKey   bool   `json:"has_key"`
}

// Snapshot is the fleet state a question is answered against.
type Snapshot struct {
	Now      time.Time
	Policy   engine.Policy
	Nodes    []model.Node
	Analysis engine.Analysis
}

// Copilot is safe for concurrent use.
type Copilot struct {
	mu     sync.RWMutex
	cfg    Config
	client *http.Client
}

// New creates a copilot. An empty BaseURL means rules-only.
func New(cfg Config) *Copilot {
	return &Copilot{
		cfg: cfg,
		client: &http.Client{
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           (&net.Dialer{Timeout: 4 * time.Second}).DialContext,
				ResponseHeaderTimeout: 90 * time.Second,
			},
		},
	}
}

// SetConfig replaces the endpoint configuration at runtime.
func (c *Copilot) SetConfig(cfg Config) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cfg.APIKey == "" && cfg.BaseURL == c.cfg.BaseURL {
		cfg.APIKey = c.cfg.APIKey // keep the key when only other fields change
	}
	c.cfg = cfg
}

func (c *Copilot) config() Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg
}

// Status describes the current configuration without leaking the key.
func (c *Copilot) Status() Status {
	cfg := c.config()
	s := Status{Enabled: cfg.BaseURL != "", Model: cfg.Model, BaseURL: cfg.BaseURL, HostNode: cfg.HostNode, HasKey: cfg.APIKey != ""}
	if !s.Enabled {
		return s
	}
	s.Locality = "unknown"
	if u, err := url.Parse(cfg.BaseURL); err == nil && u.Host != "" {
		s.Endpoint = u.Host
		s.Locality = locality(u.Hostname())
	}
	return s
}

// locality says whether an endpoint is on this machine, on a private network,
// or out on the public internet. Fleet telemetry going to a public endpoint
// is exactly what a sovereign deployment wants to know about.
func locality(host string) string {
	if strings.EqualFold(host, "localhost") {
		return "local"
	}
	ips := []net.IP{}
	if ip := net.ParseIP(host); ip != nil {
		ips = append(ips, ip)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(addrs) == 0 {
			return "unknown"
		}
		for _, a := range addrs {
			ips = append(ips, a.IP)
		}
	}
	allLocal, allPrivate := true, true
	for _, ip := range ips {
		if !ip.IsLoopback() {
			allLocal = false
		}
		if !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
			allPrivate = false
		}
	}
	switch {
	case allLocal:
		return "local"
	case allPrivate:
		return "private"
	default:
		return "external"
	}
}

func endpoint(base, path string) string {
	base = strings.TrimRight(base, "/")
	base = strings.TrimSuffix(base, "/chat/completions")
	return base + path
}

// Test checks that the endpoint answers and, if no model is set, returns the
// first model it offers.
func (c *Copilot) Test(ctx context.Context) (string, error) {
	cfg := c.config()
	if cfg.BaseURL == "" {
		return "", fmt.Errorf("no endpoint configured")
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint(cfg.BaseURL, "/models"), nil)
	if err != nil {
		return "", err
	}
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("endpoint answered %s", resp.Status)
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if len(out.Data) > 0 {
		return out.Data[0].ID, nil
	}
	return "", nil
}

// Answer is a response ready to be streamed.
type Answer struct {
	Source string // "model" or "rules"
	Model  string
	Note   string // why rules were used, when they were
	stream func(emit func(string)) error
}

// Stream writes the answer through emit, chunk by chunk.
func (a *Answer) Stream(emit func(string)) error { return a.stream(emit) }

const systemPrompt = `You are UniServe Copilot, the operations assistant for a mixed-vendor GPU fleet. You run on infrastructure the operator controls.

Answer from the fleet data in the user message and nothing else.
- Lead with the answer in one sentence, then the evidence.
- Name specific nodes and GPUs (for example isb-h200-01 GPU 2).
- The FINDINGS and WHAT-IF sections are computed exactly. Quote their numbers; do not redo the arithmetic.
- Money is in USD, from the hourly rates configured per node.
- When asked about a vendor error code, say in plain language what it means, what is affected, and the next step.
- If the data does not contain the answer, say so plainly.
- At most 170 words. Short markdown only: **bold** for key figures, "-" bullets for steps. No tables, no headings.`

// Ask prepares an answer. It never fails: if the model cannot be used the
// rules engine answers and Note explains why.
func (c *Copilot) Ask(ctx context.Context, question string, snap Snapshot) *Answer {
	cfg := c.config()
	if cfg.BaseURL == "" {
		return rulesAnswer(question, snap, "")
	}
	body, _ := json.Marshal(map[string]any{
		"model":       cfg.Model,
		"stream":      true,
		"temperature": 0.2,
		"max_tokens":  900,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": Context(snap) + "\nQUESTION: " + question},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(cfg.BaseURL, "/chat/completions"), bytes.NewReader(body))
	if err != nil {
		return rulesAnswer(question, snap, "Model endpoint is misconfigured: "+err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return rulesAnswer(question, snap, "Model endpoint unreachable ("+shortErr(err)+").")
	}
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		resp.Body.Close()
		return rulesAnswer(question, snap, fmt.Sprintf("Model endpoint answered %s %s.", resp.Status, strings.TrimSpace(string(msg))))
	}
	return &Answer{Source: "model", Model: cfg.Model, stream: func(emit func(string)) error {
		defer resp.Body.Close()
		return relay(resp.Body, emit)
	}}
}

// relay reads an OpenAI-style SSE stream and emits the visible text,
// dropping any <think> block a reasoning model produces.
func relay(r io.Reader, emit func(string)) error {
	f := &thinkFilter{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	started := false
	out := func(s string) {
		if !started {
			s = strings.TrimLeft(s, " \n\r\t")
			if s == "" {
				return
			}
			started = true
		}
		emit(s)
	}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil || len(chunk.Choices) == 0 {
			continue
		}
		text := chunk.Choices[0].Delta.Content
		if text == "" {
			text = chunk.Choices[0].Message.Content
		}
		if text == "" {
			continue
		}
		if s := f.Feed(text); s != "" {
			out(s)
		}
	}
	if s := f.Flush(); s != "" {
		out(s)
	}
	if !started {
		emit("The model returned an empty answer.")
	}
	return sc.Err()
}

// thinkFilter removes <think>...</think> spans from a token stream, coping
// with tags split across chunks.
type thinkFilter struct {
	buf     string
	inThink bool
}

const openTag, closeTag = "<think>", "</think>"

func (f *thinkFilter) Feed(s string) string {
	f.buf += s
	var out strings.Builder
	for {
		if f.inThink {
			i := strings.Index(f.buf, closeTag)
			if i < 0 {
				if keep := partialSuffix(f.buf, closeTag); keep < len(f.buf) {
					f.buf = f.buf[len(f.buf)-keep:]
				}
				return out.String()
			}
			f.buf = f.buf[i+len(closeTag):]
			f.inThink = false
			continue
		}
		if i := strings.Index(f.buf, openTag); i >= 0 {
			out.WriteString(f.buf[:i])
			f.buf = f.buf[i+len(openTag):]
			f.inThink = true
			continue
		}
		keep := partialSuffix(f.buf, openTag)
		out.WriteString(f.buf[:len(f.buf)-keep])
		f.buf = f.buf[len(f.buf)-keep:]
		return out.String()
	}
}

func (f *thinkFilter) Flush() string {
	if f.inThink {
		return ""
	}
	s := f.buf
	f.buf = ""
	return s
}

// partialSuffix returns how many trailing bytes of s could be the start of tag.
func partialSuffix(s, tag string) int {
	max := len(tag) - 1
	if len(s) < max {
		max = len(s)
	}
	for k := max; k > 0; k-- {
		if strings.HasSuffix(s, tag[:k]) {
			return k
		}
	}
	return 0
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && i+2 < len(s) {
		s = s[i+2:]
	}
	return s
}

// Context renders the snapshot as compact text for the model. Compact matters:
// a whole fleet of a few dozen GPUs fits in a couple of thousand tokens, which
// means no retrieval and no tool calling are needed.
func Context(s Snapshot) string {
	var b strings.Builder
	k := s.Analysis.KPIs
	fmt.Fprintf(&b, "FLEET SNAPSHOT at %s\n", s.Now.UTC().Format("2006-01-02 15:04 UTC"))
	fmt.Fprintf(&b, "Home jurisdiction: %s. Policy: workloads with restricted data must run on nodes in %s.\n", s.Policy.Home, s.Policy.Home)
	fmt.Fprintf(&b, "Totals: %d GPUs on %d nodes; average utilization %.0f%%; %d active, %d held idle (hoarded), %d free, %d faulted. Fleet cost $%.0f/day; idle-held waste $%.0f/day ($%.0f so far). %.0f%% of GPUs are in %s.\n",
		k.GPUs, k.Nodes, k.AvgUtil, k.Active+k.Idle, k.Hoarded, k.Free, k.Faulted, k.FleetCostPerDay, k.WastePerDay, k.WastedSoFar, k.InCountryPct, s.Policy.Home)
	b.WriteString("Vendors:")
	for _, v := range k.ByVendor {
		fmt.Fprintf(&b, " %s %d GPUs (%.0f%%);", v.Vendor, v.GPUs, v.Pct)
	}
	b.WriteString("\n\nNODES\n")
	for _, n := range s.Nodes {
		status := "online"
		if !n.Online {
			status = "OFFLINE"
		}
		fmt.Fprintf(&b, "%s | %s %s | %s | %s, %s | $%.2f per GPU-hour | %s\n", n.ID, n.Vendor, n.Accelerator, n.Provider, n.Site, n.Jurisdiction, n.CostPerGPUHour, status)
		for _, g := range n.GPUs {
			fmt.Fprintf(&b, "  GPU %d: state=%s util=%.0f%% mem=%d/%dGB temp=%.0fC", g.Index, g.State, g.UtilPct, model.MemGB(g.MemUsedMB), model.MemGB(g.MemTotalMB), g.TempC)
			if g.Workload != nil {
				fmt.Fprintf(&b, " workload=%q owner=%s kind=%s data=%s", g.Workload.Name, g.Workload.Owner, g.Workload.Kind, g.Workload.DataClass)
			}
			if g.State == model.StateHoarded && g.IdleSince != nil {
				fmt.Fprintf(&b, " idle_for=%s", engine.HumanDuration(s.Now.Sub(*g.IdleSince)))
			}
			if g.Health != model.HealthOK {
				fmt.Fprintf(&b, " health=%s code=%q vendor_message=%q", g.Health, g.HealthCode, g.HealthRaw)
			}
			if g.Drained {
				b.WriteString(" drained=yes")
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\nFINDINGS (computed)\n")
	if len(s.Analysis.Findings) == 0 {
		b.WriteString("None.\n")
	}
	for _, f := range s.Analysis.Findings {
		fmt.Fprintf(&b, "- [%s/%s] %s. %s", f.Type, f.Severity, f.Title, f.Detail)
		if f.WastePerDay > 0 {
			fmt.Fprintf(&b, " Costs $%.0f/day, $%.0f so far.", f.WastePerDay, f.WastedSoFar)
		}
		if f.Plan != nil {
			fmt.Fprintf(&b, " Proposed: %s. %s", f.Plan.Summary, strings.Join(f.Plan.Notes, " "))
		}
		b.WriteString("\n")
	}
	b.WriteString("\nWHAT-IF (computed)\n")
	for _, w := range s.Analysis.WhatIfs {
		fmt.Fprintf(&b, "- %s (%d GPUs lost): %s\n", w.Label, w.GPUsLost, w.Verdict)
		for _, m := range w.Placed {
			fmt.Fprintf(&b, "    %s (%d GB, %s) from %s -> %s [%s]%s%s\n", m.Workload, m.NeedGB, m.DataClass, m.From, m.To, m.ToVendor,
				tern(m.ViaReclaim, " after reclaiming it", ""), tern(m.CrossVendor, ", needs a build for that vendor", ""))
		}
		for _, m := range w.Unplaced {
			fmt.Fprintf(&b, "    %s (%d GB, %s) from %s -> NO CAPACITY\n", m.Workload, m.NeedGB, m.DataClass, m.From)
		}
	}
	return b.String()
}

func tern(c bool, a, b string) string {
	if c {
		return a
	}
	return b
}
