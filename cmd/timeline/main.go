package main

import (
	"butterfly.orx.me/core"
	butterflyapp "butterfly.orx.me/core/app"
	"context"
	timelineapp "github.com/kongken/mono/internal/timeline/app"
	"github.com/kongken/mono/internal/timeline/config"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	cfg := new(config.AppConfig)
	h := timelineapp.New(cfg)
	ns := os.Getenv("BUTTERFLY_NAMESPACE")
	if ns == "" {
		ns = "apps"
	}
	svc := core.New(&butterflyapp.Config{Service: "timeline", Namespace: ns, Config: cfg, Router: h.Routes, InitFunc: []func() error{func() error { return h.Bootstrap(context.Background()) }}, TeardownFunc: []func() error{h.Shutdown}})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		if err := h.Shutdown(); err != nil {
			slog.Error("close backend resources", "error", err)
		}
		os.Exit(0)
	}()
	svc.Run()
}
