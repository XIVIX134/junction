// junctiond: the Junction gateway service (v0, watch-only).
//
// It reads sing-box's Clash API, vpn-failoverd's status and the ARP table,
// records per-device usage in SQLite, and serves the web app plus a JSON API.
// It never changes routing.
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	_ "time/tzdata" // Alpine has no tzdata package installed
)

//go:embed all:web/dist
var webDist embed.FS

func main() {
	cfgPath := flag.String("config", "/etc/junction/config.json", "config file")
	resetPw := flag.Bool("reset-password", false, "forget the sign-in password, turn sign-in off, and exit")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	store, err := openStore(cfg.DB)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	if *resetPw {
		if err := resetPassword(store); err != nil {
			log.Fatalf("reset password: %v", err)
		}
		log.Printf("password removed and sign-in turned off; restart junctiond if it is running")
		return
	}
	base := cfg
	applySavedSettings(&cfg, store)
	detected := cfg.resolve()
	state, err := newState(cfg, store)
	if err != nil {
		log.Fatalf("state: %v", err)
	}
	state.base = base
	if detected {
		t := time.Now()
		state.gwDetectedAt = &t
	}
	log.Printf("junctiond %s: gateway %s, LAN %s, timezone %s", version, cfg.GatewayIP, cfg.LAN, cfg.TimeZone)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var exitCode atomic.Int32
	state.restart = func(why string) {
		// The supervisor (supervise-daemon / systemd) starts Junction again.
		log.Printf("restarting to apply %s; traffic is not affected", why)
		exitCode.Store(1)
		stop()
	}
	if cfg.FailoverEnabled {
		fo, err := loadFailover(cfg.FailoverConfig, state)
		if err != nil {
			log.Fatalf("failover: %v", err)
		}
		state.fo = fo
		go fo.run(ctx)
		log.Printf("failover: running (%d endpoints)", len(fo.cfg.Endpoints))
	}
	state.run(ctx)
	if cfg.RouterURL != "" {
		go state.every(ctx, 10*time.Minute, state.pollRouter)
	}

	web, err := fs.Sub(webDist, "web/dist")
	if err != nil {
		log.Fatalf("web: %v", err)
	}
	mux := http.NewServeMux()
	(&API{s: state}).routes(mux, web)
	srv := &http.Server{Addr: cfg.Listen, Handler: state.auth.middleware(mux), ReadHeaderTimeout: 10 * time.Second}

	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	log.Printf("junctiond listening on %s", cfg.Listen)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("http: %v", err)
	}
	state.flush() // keep the last 30 s of usage
	log.Printf("junctiond stopped")
	os.Exit(int(exitCode.Load()))
}
