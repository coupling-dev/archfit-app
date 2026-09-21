// Command reporter is the Archfit App process: a JSON logger, app.Load, app.Run. Nothing else lives here.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/alexei-led/archfit-app/internal/app"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Getenv, log)
	stop()
	if err != nil {
		log.Error("exit", "error", err.Error())
		os.Exit(1)
	}
}

func run(ctx context.Context, getenv func(string) string, log *slog.Logger) error {
	c, err := app.Load(getenv)
	if err != nil {
		return err
	}
	return app.Run(ctx, c, log)
}
