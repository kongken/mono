package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"butterfly.orx.me/core"
	butterflyapp "butterfly.orx.me/core/app"
	metroapp "github.com/kongken/mono/internal/metro/app"
	"github.com/kongken/mono/internal/metro/config"
)

func main() {
	cfg := new(config.AppConfig)
	handlers := metroapp.New(cfg)
	namespace := os.Getenv("BUTTERFLY_NAMESPACE")
	if namespace == "" {
		namespace = "apps"
	}
	service := core.New(&butterflyapp.Config{Service: "metro", Namespace: namespace, Config: cfg, Router: handlers.Routes, InitFunc: []func() error{func() error { return handlers.Bootstrap(context.Background()) }}, TeardownFunc: []func() error{handlers.Shutdown}})
	// This Butterfly version does not invoke TeardownFunc on OS signals itself.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signals
		if err := handlers.Shutdown(); err != nil {
			slog.Error("close backend resources", "error", err)
		}
		os.Exit(0)
	}()
	slog.Info("starting metro backend", "namespace", namespace)
	service.Run()
}
