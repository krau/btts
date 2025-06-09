package config

import (
	"strings"

	"github.com/charmbracelet/log"

	"github.com/spf13/viper"
)

type AppConfig struct {
	AppID    int     `toml:"app_id" mapstructure:"app_id"`
	AppHash  string  `toml:"app_hash" mapstructure:"app_hash"`
	BotToken string  `toml:"bot_token" mapstructure:"bot_token"`
	Admins   []int64 `toml:"admins" mapstructure:"admins"`
	Engine   struct {
		Url      string `toml:"url" mapstructure:"url"`
		Key      string `toml:"key" mapstructure:"key"`
		Embedder struct {
			Name             string `toml:"name" mapstructure:"name"`
			Source           string `toml:"source" mapstructure:"source"`
			Model            string `toml:"model" mapstructure:"model"`
			ApiKey           string `toml:"api_key" mapstructure:"api_key"`
			DocumentTemplate string `toml:"document_template" mapstructure:"document_template"`
			Dimensions       int    `toml:"dimensions" mapstructure:"dimensions"`
			URL              string `toml:"url" mapstructure:"url"`
		} `toml:"embedder" mapstructure:"embedder"`
	} `toml:"engine" mapstructure:"engine"`
	Api struct {
		Enable bool   `toml:"enable" mapstructure:"enable"`
		Addr   string `toml:"addr" mapstructure:"addr"`
		Key    string `toml:"key" mapstructure:"key"`
	}
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
