package cmd

import (
	"context"
	"os"
	"os/signal"
	"time"

	_ "github.com/ncruces/go-sqlite3/embed"

	"github.com/charmbracelet/log"
	"github.com/krau/btts/bot"
	"github.com/krau/btts/database"
	"github.com/krau/btts/engine"
	"github.com/krau/btts/userclient"
)

func run() {
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
	engine, err := engine.NewEngine(ctx)
	if err != nil {
		log.Errorf("Failed to create engine: %v", err)
		return
	}

	userClient, err := userclient.NewUserClient(ctx)
	if err != nil {
		log.Errorf("Failed to create user client: %v", err)
		return
	}

	bot, err := bot.NewBot(ctx, userClient, engine)
	if err != nil {
		log.Errorf("Failed to create bot: %v", err)
		return
	}
	bot.Start(ctx)
}
