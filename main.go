// UniServe: one view and one copilot for a mixed-vendor GPU fleet.
//
//	uniserve server   run the control plane, console and simulator
//	uniserve agent    run on a GPU node and report to a control plane
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"uniserve/internal/agent"
	"uniserve/internal/copilot"
	"uniserve/internal/engine"
	"uniserve/internal/server"
	"uniserve/internal/sim"
	"uniserve/internal/store"
)

var version = "0.1.0" // overridden at build time with -X main.version

func main() {
	args := os.Args[1:]
	cmd := "server"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd {
	case "server":
		err = runServer(ctx, args)
	case "agent":
		err = runAgent(ctx, args)
	case "version":
		fmt.Println("uniserve", version)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil && !errors.Is(err, flag.ErrHelp) {
		log.Fatalf("uniserve: %v", err)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `UniServe `+version+`

Usage:
  uniserve server [flags]   control plane, web console and simulator
  uniserve agent  [flags]   run on a GPU node; reports to a control plane
  uniserve version

Run "uniserve server -h" or "uniserve agent -h" for flags.
`)
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func runServer(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	addr := fs.String("addr", env("UNISERVE_ADDR", ":8080"), "listen address")
	home := fs.String("home", env("UNISERVE_HOME", "PK"), "home jurisdiction (ISO country code); restricted data must stay here")
	simOn := fs.Bool("sim", true, "run the built-in simulated fleet")
	idleAfter := fs.Duration("idle-after", 10*time.Minute, "how long an allocated GPU may sit quiet before it counts as held idle")
	offlineAfter := fs.Duration("offline-after", 20*time.Second, "how long a node may go without reporting before it counts as offline")
	token := fs.String("token", env("UNISERVE_TOKEN", ""), "shared token agents must present (optional)")
	llmURL := fs.String("llm-url", env("UNISERVE_LLM_URL", ""), "OpenAI-compatible base URL for the copilot, e.g. http://10.0.0.7:8000/v1")
	llmModel := fs.String("llm-model", env("UNISERVE_LLM_MODEL", ""), "model name to request")
	llmKey := fs.String("llm-key", env("UNISERVE_LLM_KEY", ""), "API key for the endpoint (optional)")
	llmNode := fs.String("llm-node", env("UNISERVE_LLM_NODE", ""), "id of the fleet node serving the copilot model (optional, shown in the console)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st := store.New(store.Options{IdleAfter: *idleAfter, OfflineAfter: *offlineAfter})
	srv := &server.Server{
		Store:   st,
		Copilot: copilot.New(copilot.Config{BaseURL: *llmURL, Model: *llmModel, APIKey: *llmKey, HostNode: *llmNode}),
		Policy:  engine.Policy{Home: strings.ToUpper(*home)},
		Token:   *token,
		Version: version,
	}
	if *simOn {
		srv.Sim = sim.New(st)
		go srv.Sim.Run(ctx, 2*time.Second)
	}
	go srv.RecordHistory(ctx, 2*time.Second)

	hs := &http.Server{Addr: *addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
	}()
	shown := *addr
	if strings.HasPrefix(shown, ":") {
		shown = "localhost" + shown
	}
	log.Printf("uniserve %s: console at http://%s  (home=%s, simulator=%v, copilot=%s)", version, shown, srv.Policy.Home, *simOn, copilotDesc(srv.Copilot.Status()))
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func copilotDesc(s copilot.Status) string {
	if !s.Enabled {
		return "rules only (no model configured)"
	}
	return fmt.Sprintf("%s @ %s", s.Model, s.Endpoint)
}

func runAgent(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	cfg := agent.Config{}
	fs.StringVar(&cfg.Server, "server", env("UNISERVE_SERVER", ""), "control plane URL, e.g. http://10.0.0.5:8080")
	fs.StringVar(&cfg.Token, "token", env("UNISERVE_TOKEN", ""), "shared agent token, if the server requires one")
	fs.StringVar(&cfg.NodeID, "node", "", "node id shown in the console (default: hostname)")
	fs.StringVar(&cfg.Provider, "provider", "On-prem", "who operates this hardware")
	fs.StringVar(&cfg.Site, "site", "", "city or facility")
	fs.StringVar(&cfg.Jurisdiction, "jurisdiction", "PK", "ISO country code this node sits in")
	fs.Float64Var(&cfg.CostPerHour, "cost", 0, "cost per GPU-hour in USD, used for waste figures")
	fs.StringVar(&cfg.DataClass, "data-class", "", "data class of workloads on this node: restricted, internal or public")
	fs.StringVar(&cfg.Tool, "tool", "auto", "vendor tool: auto, nvidia or ascend")
	fs.StringVar(&cfg.ReplayFile, "replay", "", "read a captured tool output from this file instead of running the tool")
	fs.DurationVar(&cfg.Interval, "interval", 5*time.Second, "how often to report")
	fs.BoolVar(&cfg.Once, "once", false, "report once and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return agent.Run(ctx, cfg)
}
