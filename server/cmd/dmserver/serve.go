package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/server/internal/runtime"
)

// serve attaches process signals; runtime owns startup and ordered shutdown.
func serve(ctx context.Context, cfg app.Config) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runtime.Serve(ctx, cfg)
}

const shutdownTimeout = 10 * time.Second
