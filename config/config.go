package config

import (
	"strings"

	"github.com/charmbracelet/log"

	"github.com/spf13/viper"
)

type AppConfig struct {
	AppID    int    `toml:"app_id" mapstructure:"app_id"`
	AppHash  string `toml:"app_hash" mapstructure:"app_hash"`
	BotToken string `toml:"bot_token" mapstructure:"bot_token"`
	Admins   []int64  `toml:"admins" mapstructure:"admins"`
	Engine   struct {
		Url string `toml:"url" mapstructure:"url"`
		Key string `toml:"key" mapstructure:"key"`
	} `toml:"engine" mapstructure:"engine"`
}

var C AppConfig

func init() {
	viper.SetConfigName("config")
	viper.SetConfigType("toml")
	viper.AddConfigPath(".")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.SetEnvPrefix("btts")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Error reading config file, %s", err)
	}
	if err := viper.Unmarshal(&C); err != nil {
		log.Fatalf("Unable to decode into struct, %v", err)
	}
}
