package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"runtime/debug"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/pelletier/go-toml"
	"github.com/sirupsen/logrus"
)

const (
	packetTypeReferencePackage      = "github.com/sandertv/gophertunnel"
	packetTypeReferenceLinkTemplate = "(Look at https://pkg.go.dev/" + packetTypeReferencePackage + "@%s/minecraft/protocol/packet#pkg-index)"
)

var packetTypeReferenceLink string

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

// findPacketTypeReferencePackageVersion generates a pkg.go.dev URL for the reference of packet type.
// It gets the version of one specified library (Gophertunnel) that was shipped in the current build.
// Generated URL will be output to packetTypeReferenceLink eventually.
// If it fails to read the build info, the "latest" version will be used.
func findPacketTypeReferencePackageVersion() {
	if bi, ok := debug.ReadBuildInfo(); !ok {
		logrus.Warn("Failed to read build info")
		return
	} else {
		for _, dep := range bi.Deps {
			if dep.Path != packetTypeReferencePackage {
				continue
			}

			packetTypeReferenceLink = fmt.Sprintf(packetTypeReferenceLinkTemplate, dep.Version)
			return
		}
	}

	packetTypeReferenceLink = fmt.Sprintf(packetTypeReferenceLinkTemplate, "latest")
}
