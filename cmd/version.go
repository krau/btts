package cmd

import (
	"fmt"
	"runtime"

	"github.com/blang/semver"
	"github.com/charmbracelet/log"
	"github.com/rhysd/go-github-selfupdate/selfupdate"
	"github.com/spf13/cobra"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

var VersionCmd = &cobra.Command{
	Use:     "version",
	Aliases: []string{"v"},
	Short:   "Print the version number of btts",
	Run: func(cmd *cobra.Command, args []string) {
		log.Infof("btts version: %s %s/%s\nBuildTime: %s, Commit: %s\n", Version, runtime.GOOS, runtime.GOARCH, BuildTime, GitCommit)
	},
}

var upgradeCmd = &cobra.Command{
	Use:     "upgrade",
	Aliases: []string{"up"},
	Short:   "Upgrade btts to the latest version",
	Run: func(cmd *cobra.Command, args []string) {
		v := semver.MustParse(Version)
		latest, err := selfupdate.UpdateSelf(v, "krau/btts")
		if err != nil {
			log.Error("Binary update failed:", err)
			return
		}
		if latest.Version.Equals(v) {
			log.Infof("Current binary is the latest version %s", Version)
		} else {
			log.Infof("Successfully updated to version %s", latest.Version)
			fmt.Println("Release note:\n", latest.ReleaseNotes)
		}
	},
}

func init() {
	rootCmd.AddCommand(VersionCmd)
	rootCmd.AddCommand(upgradeCmd)
}
