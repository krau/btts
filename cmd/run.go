package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/charmbracelet/log"
	"github.com/krau/btts/api"
	"github.com/krau/btts/bot"
	"github.com/krau/btts/cmd/migrate"
	"github.com/krau/btts/config"
	"github.com/krau/btts/database"
	"github.com/krau/btts/engine"
	"github.com/krau/btts/userclient"
)

func run() error {
	config.Init()
	logger := log.NewWithOptions(os.Stdout, log.Options{
		Level:           log.DebugLevel,
		ReportTimestamp: true,
		TimeFormat:      time.TimeOnly,
		ReportCaller:    true,
	})
	if err := os.MkdirAll("data", os.ModePerm); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	ctx = log.WithContext(ctx, logger)

	if err := database.InitDatabase(ctx); err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}

	userClient, err := userclient.NewUserClient(ctx)
	if err != nil {
		return fmt.Errorf("create user client: %w", err)
	}
	defer func() {
		if err := userClient.Close(); err != nil {
			logger.Errorf("Failed to close user client: %v", err)
		}
	}()

	engine, err := engine.NewEngine(ctx)
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	bot, err := bot.NewBot(ctx, userClient, engine)
	if err != nil {
		return fmt.Errorf("create bot: %w", err)
	}
	if backgroundMigrate || backgroundMigrateDropOld {
		go func() {
			logger.Info("Starting smooth migration in background")
			if err := migrate.MigrateToV1(ctx, backgroundMigrateDropOld); err != nil {
				logger.Error("Smooth migration failed", "error", err)
			} else {
				logger.Info("Smooth migration completed successfully")
			}
		}()
	}

	if config.C.Api.Enable {
		api.Serve(config.C.Api.Addr)
		log.Infof("API server started at %s", config.C.Api.Addr)
	}
	return bot.Start(ctx)
}
