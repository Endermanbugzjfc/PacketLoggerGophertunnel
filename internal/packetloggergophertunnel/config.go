package main

import (
	"io/ioutil"
	"log"
	"os"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/pelletier/go-toml"
	"github.com/sirupsen/logrus"
)

type config struct {
	Connection struct {
		LocalAddress  string
		RemoteAddress string
	}
	PacketLogger struct {
		ShowPacketType               []string
		ReportHiddenPacketCountDelay struct {
			Receive, Send time.Duration
		}
	}
	Reload struct {
		ConfigAutoReload bool
	}
}

func readConfig(configPath string) config {
	c := config{}
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		f, err := os.Create(configPath)
		if err != nil {
			log.Fatalf("error creating config: %v", err)
		}

		// Default config:
		c.PacketLogger.ShowPacketType = []string{
			"ActorEvent",
			"ActorPickRequest",
			packetTypeReferenceLink,
		}
		const delay = time.Second * 5
		c.PacketLogger.ReportHiddenPacketCountDelay = struct {
			Receive, Send time.Duration
		}{delay, delay}

		data, err := toml.Marshal(c)
		if err != nil {
			log.Fatalf("error encoding default config: %v", err)
		}
		if _, err := f.Write(data); err != nil {
			log.Fatalf("error writing encoded default config: %v", err)
		}
		_ = f.Close()
	}
	if readConfigNoWrite(configPath, &c) != nil {
		logrus.Fatal("Please come back with a fixed config.")
	}

	// Fallback config:
	if c.Connection.LocalAddress == "" {
		c.Connection.LocalAddress = "0.0.0.0:19132"
	}
	if len(c.PacketLogger.ShowPacketType) == 0 {
		c.PacketLogger.ShowPacketType = []string{
			packetTypeReferenceLink,
		}
	}

	data, _ := toml.Marshal(c)
	if err := ioutil.WriteFile(configPath, data, 0644); err != nil {
		log.Fatalf("error writing config file: %v", err)
	}
	return c
}

func readConfigNoWrite(configPath string, c *config) error {
	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		logrus.Errorf("error reading config: %v", err)
	}
	if err := toml.Unmarshal(data, c); err != nil {
		logrus.Errorf("error decoding config: %v", err)

		return err
	}

	return nil
}

// configAutoReload runs each onReload function on its own goroutine.
func configAutoReload(configPath string, watcher *fsnotify.Watcher, onReload func(c config)) {
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			c := config{}
			if event.Op&fsnotify.Write != fsnotify.Write || readConfigNoWrite(configPath, &c) != nil {
				continue
			}
			logrus.Debug("Reloaded config.")

			if !c.Reload.ConfigAutoReload {
				logrus.Info("Config auto-reload will until this app instance ends from now on.")
				return
			}

			onReload(c)
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			logrus.Warnf("Failed to reload config: %s", err)
		}
	}
}
