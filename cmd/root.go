package cmd

import (
	"github.com/charmbracelet/log"
	"github.com/krau/btts/cmd/migrate"
	"github.com/spf13/cobra"
)

var (
	backgroundMigrate        bool
	backgroundMigrateDropOld bool
)

var rootCmd = &cobra.Command{
	Use: "btts",
	RunE: func(cmd *cobra.Command, args []string) error {
		return run()
	},
}

func init() {
	rootCmd.Flags().BoolVar(&backgroundMigrate, "migrate", false, "Run migration in background during startup")
	rootCmd.Flags().BoolVar(&backgroundMigrateDropOld, "migrate-drop-old", false, "Drop old indexes after background migration")
	migrate.RegisterCmd(rootCmd)
	RegisterTakeoutCmd(rootCmd)
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
