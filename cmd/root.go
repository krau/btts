package cmd

import (
	"github.com/charmbracelet/log"
	"github.com/krau/btts/cmd/migrate"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use: "btts",
	Run: func(cmd *cobra.Command, args []string) {
		run()
	},
}

func init() {
	migrate.RegisterCmd(rootCmd)
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
