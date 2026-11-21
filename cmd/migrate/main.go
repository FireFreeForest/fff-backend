package main

import (
	"fmt"
	"os"

	"firefreeforest/backend/internal/config"
	"firefreeforest/backend/internal/platform/logger"
	"firefreeforest/backend/internal/platform/postgres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

// run applies all pending database migrations and exits. It is meant to be run
// as a separate deploy step before the API and worker start, so no service is
// querying the schema while DDL is in progress.
func run() error {
	cfg, err := config.LoadMigrate()
	if err != nil {
		return err
	}

	logFormat := "text"
	if cfg.IsProduction() {
		logFormat = "json"
	}
	log := logger.New(cfg.LogLevel, logFormat)

	version, dirty, err := postgres.MigrateWithVersion(cfg.DBDSN)
	if err != nil {
		return err
	}

	log.Info("database migrations applied", "version", version, "dirty", dirty)
	return nil
}
