// Package server is the control plane's HTTP surface: the ingest endpoint
// agents push to, the JSON API the console reads, and the console itself.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"uniserve/internal/copilot"
	"uniserve/internal/engine"
	"uniserve/internal/model"
	"uniserve/internal/sim"
	"uniserve/internal/store"
	"uniserve/web"
)

// Server wires the pieces together.
type Server struct {
	Store   *store.Store
	Sim     *sim.Sim // nil when the simulator is disabled
	Copilot *copilot.Copilot
	Policy  engine.Policy
	Token   string // if set, agents must present it
	Version string
}

var jurisdictionNames = map[string]string{
	"PK": "Pakistan", "SA": "Saudi Arabia", "AE": "UAE", "DE": "Germany", "US": "United States",
	"CN": "China", "MY": "Malaysia", "GB": "United Kingdom", "SG": "Singapore", "QA": "Qatar",
	"TR": "Türkiye", "IN": "India", "NL": "Netherlands", "FR": "France", "IE": "Ireland",
}

// Handler returns the full HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok\n") })
	mux.HandleFunc("GET /api/v1/state", s.handleState)
	mux.HandleFunc("POST /api/v1/ingest", s.handleIngest)
	mux.HandleFunc("POST /api/v1/copilot", s.handleCopilot)
	mux.HandleFunc("POST /api/v1/actions", s.handleAction)
	mux.HandleFunc("POST /api/v1/sim/fault", s.handleFault)
	mux.HandleFunc("POST /api/v1/sim/reset", s.handleReset)
	mux.HandleFunc("GET /api/v1/settings/llm", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, s.Copilot.Status()) })
	mux.HandleFunc("POST /api/v1/settings/llm", s.handleLLMSettings)
	mux.Handle("/", http.FileServer(http.FS(web.Static())))
	return mux
}

// RecordHistory samples fleet utilization until ctx is cancelled.
func (s *Server) RecordHistory(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			nodes := s.Store.Nodes()
			if len(nodes) > 0 {
				s.Store.RecordUtil(engine.Analyze(nodes, s.Policy, time.Now()).KPIs.AvgUtil)
			}
		}
	}
}

func (s *Server) snapshot() copilot.Snapshot {
	now := time.Now()
	nodes := s.Store.Nodes()
	// Home jurisdiction first, then by vendor, so the picture is stable.
	sort.SliceStable(nodes, func(i, j int) bool {
		hi, hj := nodes[i].Jurisdiction == s.Policy.Home, nodes[j].Jurisdiction == s.Policy.Home
		if hi != hj {
			return hi
		}
		return nodes[i].ID < nodes[j].ID
	})
	a := engine.Analyze(nodes, s.Policy, now)
	for i := range a.Findings {
		if p := a.Findings[i].Plan; p != nil {
			p.Executable = s.simulated(p.Source) && (p.Target == "" || s.simulated(p.Target))
		}
	}
	return copilot.Snapshot{Now: now, Policy: s.Policy, Nodes: nodes, Analysis: a}
}

func (s *Server) simulated(gpuID string) bool {
	if s.Sim == nil {
		return false
	}
	i := strings.LastIndex(gpuID, ":")
	return i > 0 && s.Sim.Owns(gpuID[:i])
}

func (s *Server) handleState(w http.ResponseWriter, _ *http.Request) {
	snap := s.snapshot()
	simInfo := map[string]any{"enabled": s.Sim != nil}
	if s.Sim != nil {
		simInfo["scenarios"] = s.Sim.Scenarios()
	}
	name := jurisdictionNames[s.Policy.Home]
	if name == "" {
		name = s.Policy.Home
	}
	writeJSON(w, 200, map[string]any{
		"time":          snap.Now,
		"version":       s.Version,
		"policy":        map[string]string{"home": s.Policy.Home, "home_name": name},
		"jurisdictions": jurisdictionNames,
		"nodes":         snap.Nodes,
		"kpis":          snap.Analysis.KPIs,
		"findings":      emptyIfNil(snap.Analysis.Findings),
		"what_ifs":      snap.Analysis.WhatIfs,
		"events":        s.Store.Events(),
		"history":       s.Store.History(),
		"copilot":       s.Copilot.Status(),
		"sim":           simInfo,
		"idle_after":    engine.HumanDuration(s.Store.IdleAfter()),
	})
}

func emptyIfNil(f []engine.Finding) []engine.Finding {
	if f == nil {
		return []engine.Finding{}
	}
	return f
}

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	if s.Token != "" {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) != 1 {
			httpError(w, http.StatusUnauthorized, "missing or wrong agent token")
			return
		}
	}
	var rep model.Report
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&rep); err != nil {
		httpError(w, http.StatusBadRequest, "bad report: "+err.Error())
		return
	}
	if s.Sim != nil && s.Sim.Owns(rep.Node.ID) {
		httpError(w, http.StatusConflict, "node id "+rep.Node.ID+" is used by the simulator; pick another --node")
		return
	}
	if rep.Node.Source == "" {
		rep.Node.Source = model.SourceLive
	}
	if err := s.Store.Upsert(rep); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "gpus": len(rep.GPUs)})
}

func (s *Server) handleCopilot(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Question string `json:"question"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil || strings.TrimSpace(in.Question) == "" {
		httpError(w, http.StatusBadRequest, "send {\"question\": \"...\"}")
		return
	}
	ans := s.Copilot.Ask(r.Context(), strings.TrimSpace(in.Question), s.snapshot())
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	h.Set("X-Answer-Source", ans.Source)
	h.Set("X-Answer-Model", headerSafe(ans.Model))
	h.Set("X-Answer-Note", headerSafe(ans.Note))
	w.WriteHeader(200)
	fl, _ := w.(http.Flusher)
	_ = ans.Stream(func(chunk string) {
		io.WriteString(w, chunk)
		if fl != nil {
			fl.Flush()
		}
	})
}

func headerSafe(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || r > 126 {
			return ' '
		}
		return r
	}, s)
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FindingID string `json:"finding_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil || in.FindingID == "" {
		httpError(w, http.StatusBadRequest, "send {\"finding_id\": \"...\"}")
		return
	}
	var f *engine.Finding
	snap := s.snapshot()
	for i := range snap.Analysis.Findings {
		if snap.Analysis.Findings[i].ID == in.FindingID {
			f = &snap.Analysis.Findings[i]
		}
	}
	if f == nil || f.Plan == nil {
		httpError(w, http.StatusNotFound, "that finding no longer exists or has no plan")
		return
	}
	if !f.Plan.Executable {
		httpError(w, http.StatusConflict, "UniServe agents are read-only, so this has to be done on the node: "+f.Plan.Summary+".")
		return
	}
	var err error
	switch f.Plan.Action {
	case "reclaim":
		err = s.Sim.Reclaim(f.Plan.Source)
	case "migrate":
		err = s.Sim.Migrate(f.Plan.Source, f.Plan.Target)
	default:
		err = fmt.Errorf("unknown action %q", f.Plan.Action)
	}
	if err != nil {
		httpError(w, http.StatusConflict, err.Error())
		return
	}
	s.Store.AddEvent(model.Event{Kind: "action", Actor: "operator", Text: "Approved: " + f.Plan.Summary})
	writeJSON(w, 200, map[string]any{"ok": true, "done": f.Plan.Summary})
}

func (s *Server) handleFault(w http.ResponseWriter, r *http.Request) {
	if s.Sim == nil {
		httpError(w, http.StatusNotFound, "simulator is disabled")
		return
	}
	var in struct {
		Scenario string `json:"scenario"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in)
	sc, err := s.Sim.InjectFault(in.Scenario)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "gpu": sc.GPU})
}

func (s *Server) handleReset(w http.ResponseWriter, _ *http.Request) {
	if s.Sim == nil {
		httpError(w, http.StatusNotFound, "simulator is disabled")
		return
	}
	s.Sim.Reset()
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleLLMSettings(w http.ResponseWriter, r *http.Request) {
	var cfg copilot.Config
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&cfg); err != nil {
		httpError(w, http.StatusBadRequest, "bad settings: "+err.Error())
		return
	}
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.BaseURL != "" && !strings.HasPrefix(cfg.BaseURL, "http://") && !strings.HasPrefix(cfg.BaseURL, "https://") {
		cfg.BaseURL = "http://" + cfg.BaseURL
	}
	s.Copilot.SetConfig(cfg)
	out := map[string]any{"ok": true}
	if cfg.BaseURL != "" {
		found, err := s.Copilot.Test(r.Context())
		if err != nil {
			out["ok"] = false
			out["error"] = err.Error()
		} else if cfg.Model == "" && found != "" {
			cfg.Model = found
			s.Copilot.SetConfig(cfg)
		}
		s.Store.AddEvent(model.Event{Kind: "info", Actor: "operator", Text: "Copilot endpoint set to " + s.Copilot.Status().Endpoint})
	} else {
		s.Store.AddEvent(model.Event{Kind: "info", Actor: "operator", Text: "Copilot model disconnected; answering from rules"})
	}
	out["status"] = s.Copilot.Status()
	writeJSON(w, 200, out)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"ok": false, "error": msg})
}
