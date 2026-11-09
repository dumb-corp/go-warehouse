package logger

import (
	"fmt"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	Level string `envconfig:"LEVEL" default:"INFO"`
	File  string `envconfig:"FILE" default:"app.log"`
}

func NewConfig() Config {
	var config Config
	if err := envconfig.Process("LOGGER", &config); err != nil {
		err = fmt.Errorf("get logger error %w", err)
		panic(err)
	}

	return config
}
