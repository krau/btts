package cmd

import (
	"context"
	"os"
	"os/signal"
	"time"

	_ "github.com/ncruces/go-sqlite3/embed"

	"github.com/charmbracelet/log"
	"github.com/krau/btts/core/bot"
	"github.com/krau/btts/core/userclient"
)

func run() {

	if err := os.MkdirAll("data", os.ModePerm); err != nil {
		log.Errorf("Failed to create data directory: %v", err)
		return
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	logger := log.NewWithOptions(os.Stdout, log.Options{
		Level:           log.DebugLevel,
		ReportTimestamp: true,
		TimeFormat:      time.TimeOnly,
		ReportCaller:    true,
	})
	ctx = log.WithContext(ctx, logger)

	userClient, err := userclient.NewUserClient(ctx)
	if err != nil {
		log.Errorf("Failed to create user client: %v", err)
		return
	}
	defer func() {
		if err := userClient.Close(); err != nil {
			log.Errorf("Failed to close user client: %v", err)
		}
	}()
	bot, err := bot.NewBot(ctx, userClient)
	if err != nil {
		log.Errorf("Failed to create bot: %v", err)
		return
	}
	bot.Start(ctx)
}
