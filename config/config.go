package config

import (
	"github.com/spf13/viper"
)

type AppConfig struct {
	AppID    int    `toml:"app_id" mapstructure:"app_id"`
	AppHash  string `toml:"app_hash" mapstructure:"app_hash"`
	BotToken string `toml:"bot_token" mapstructure:"bot_token"`
}

var C AppConfig

func init() {
	viper.SetConfigName("config")
	viper.SetConfigType("toml")
	viper.AddConfigPath(".")

	if err := viper.ReadInConfig(); err != nil {
		panic(err)
	}
	if err := viper.Unmarshal(&C); err != nil {
		panic(err)
	}
}
