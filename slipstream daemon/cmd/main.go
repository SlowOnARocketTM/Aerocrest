package main

import (
	"log/slog"
	"os"
	"time"
)

func main() {
	cfg := config{
		addr:     ":5678",
		db:       dbConfig{},
		wTimeout: 30 * time.Second,
		rTimeout: 15 * time.Second,
	}
	app := api{config: cfg}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	handler := app.Mount()
	err := app.Run(handler)
	if err != nil {
		logger.Error("Server failed to start", "error", err)
		os.Exit(1)
	}
}
