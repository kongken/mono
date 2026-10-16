package main

import (
	"log/slog"
	"os"

	"butterfly.orx.me/core"
	butterflyapp "butterfly.orx.me/core/app"
	bffapp "go.orx.me/apps/trip-map/internal/bff/app"
	"go.orx.me/apps/trip-map/internal/bff/config"
)

func main() {
	cfg := new(config.Config)
	handlers := bffapp.New(cfg)
	namespace := os.Getenv("BUTTERFLY_NAMESPACE")
	if namespace == "" {
		namespace = "apps"
	}
	service := core.New(&butterflyapp.Config{Service: "bff", Namespace: namespace, Config: cfg, Router: handlers.Routes, InitFunc: []func() error{handlers.Bootstrap}})
	slog.Info("starting bff", "namespace", namespace)
	service.Run()
}
