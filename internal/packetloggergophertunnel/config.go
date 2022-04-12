package main

import (
	"os"

	"github.com/pelletier/go-toml"
	"github.com/sirupsen/logrus"
)

type config struct {
	Connection struct {
		RemoteAddress string
	}
}

func readConfig() (conf config) {
	conf = config{}
	defaultConfigMarshal, _ := toml.Marshal(conf)

	defaultConfigLog := func(defaultConfigMarshal []byte) {
		logrus.Infof(
			"Default config to copy:\n%s",
			string(defaultConfigMarshal),
		)
	}

	const path = "config.toml"
	if data, err := os.ReadFile(path); err != nil {
		defer defaultConfigLog(defaultConfigMarshal)
		logrus.Panicf(
			"Failed to read %s: %s",
			path,
			err,
		)
	} else if err := toml.Unmarshal(data, &conf); err != nil {
		defer defaultConfigLog(defaultConfigMarshal)
		logrus.Panicf(
			"Failed to parse %s: %s",
			path,
			err,
		)
	}
	return
}
