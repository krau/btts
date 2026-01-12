package cmd

import (
	"context"
	"os"
	"os/signal"
	"time"

	_ "github.com/ncruces/go-sqlite3/embed"

	"github.com/charmbracelet/log"
	"github.com/krau/btts/api"
	"github.com/krau/btts/bot"
	"github.com/krau/btts/cmd/migrate"
	"github.com/krau/btts/config"
	"github.com/krau/btts/database"
	"github.com/krau/btts/engine"
	"github.com/krau/btts/userclient"
)

func run() {
	config.Init()
	logger := log.NewWithOptions(os.Stdout, log.Options{
		Level:           log.DebugLevel,
		ReportTimestamp: true,
		TimeFormat:      time.TimeOnly,
		ReportCaller:    true,
	})
	if err := os.MkdirAll("data", os.ModePerm); err != nil {
		logger.Errorf("Failed to create data directory: %v", err)
		return
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	ctx = log.WithContext(ctx, logger)

	if err := database.InitDatabase(ctx); err != nil {
		log.Errorf("Failed to initialize database: %v", err)
		return
	}

	userClient, err := userclient.NewUserClient(ctx)
	if err != nil {
		log.Errorf("Failed to create user client: %v", err)
		return
	}

	engine, err := engine.NewEngine(ctx)
	if err != nil {
		log.Errorf("Failed to create engine: %v", err)
		return
	}

	bot, err := bot.NewBot(ctx, userClient, engine)
	if err != nil {
		log.Errorf("Failed to create bot: %v", err)
		return
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
	bot.Start(ctx)
}
