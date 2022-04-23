package main

import (
	_ "embed"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/df-mc/atomic"
	"github.com/fsnotify/fsnotify"
	_ "github.com/icza/bitio"
	"github.com/pelletier/go-toml"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/auth"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
	"github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
)

const (
	receivePrefix = "[Receive] "
	sendPrefix    = "[Send] "

	packetTypeReferencePackage      = "github.com/sandertv/gophertunnel"
	packetTypeReferenceLinkTemplate = "(Look at https://pkg.go.dev/" + packetTypeReferencePackage + "@%s/minecraft/protocol/packet#pkg-index)"
)

var (
	configAtomic                    atomic.Value[config]
	hiddenReceivePacketsCountAtomic atomic.Int32
	hiddenSendPacketsCountAtomic    atomic.Int32

	packetTypeReferenceLink string
)

// The following program implements a proxy that forwards players from one local address to a remote address.
func main() {
	findPacketTypeReferencePackageVersion()

	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})
	logrus.SetLevel(logrus.TraceLevel)

	const configPath = "config.toml"
	config := readConfig(configPath)
	configAtomic.Store(config)
	token, err := auth.RequestLiveToken()
	if err != nil {
		panic(err)
	}
	src := auth.RefreshTokenSource(token)

	p, err := minecraft.NewForeignStatusProvider(config.Connection.RemoteAddress)
	if err != nil {
		panic(err)
	}
	listener, err := minecraft.ListenConfig{
		StatusProvider: p,
	}.Listen("raknet", config.Connection.LocalAddress)
	if err != nil {
		panic(err)
	}
	defer listener.Close()

	if config.FileWatcher.ConfigAutoReload {
		logrus.Info("Creating file watcher...")
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			logrus.Error(err)
		}
		defer watcher.Close()

		logrus.Infof("Adding %s to file watcher...", configPath)

		if err := watcher.Add(configPath); err != nil {
			logrus.Error(err)
		} else {
			go configAutoReload(configPath, watcher)
		}
	}

	receiveNewDelayChannel := make(chan time.Duration)
	sendNewDelayChannel := make(chan time.Duration)

	go startRportingHiddenPacketCount(receivePrefix, receiveNewDelayChannel, &hiddenReceivePacketsCountAtomic)
	go startRportingHiddenPacketCount(sendPrefix, sendNewDelayChannel, &hiddenSendPacketsCountAtomic)

	logrus.Info("Starting local proxy...")
	for {
		c, err := listener.Accept()
		if err != nil {
			panic(err)
		}
		logrus.Info("New connection established.")
		go handleConn(c.(*minecraft.Conn), listener, config, src)
	}
}

func configAutoReload(configPath string, watcher *fsnotify.Watcher) {
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

			if !c.FileWatcher.ConfigAutoReload {
				logrus.Info("Config auto reload has been disabled for this app instance.")
				return
			}

			// TODO
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			logrus.Warnf("Failed to reload config: %s", err)
		}
	}
}

// handleConn handles a new incoming minecraft.Conn from the minecraft.Listener passed.
func handleConn(conn *minecraft.Conn, listener *minecraft.Listener, config config, src oauth2.TokenSource) {
	serverConn, err := minecraft.Dialer{
		TokenSource: src,
		ClientData:  conn.ClientData(),
	}.Dial("raknet", config.Connection.RemoteAddress)
	if err != nil {
		panic(err)
	}
	var g sync.WaitGroup
	g.Add(2)
	go func() {
		if err := conn.StartGame(serverConn.GameData()); err != nil {
			panic(err)
		}
		g.Done()
	}()
	go func() {
		if err := serverConn.DoSpawn(); err != nil {
			panic(err)
		}
		g.Done()
	}()
	g.Wait()

	go func() {
		defer logrus.Info("Terminated one connection.")
		defer listener.Disconnect(conn, "connection lost")
		defer serverConn.Close()
		for {
			pk, err := conn.ReadPacket()
			if err != nil {
				return
			}

			pkText, err := packetToLog(pk, &hiddenSendPacketsCountAtomic)
			if pkText != "" {
				text := sendPrefix + pkText
				if err == nil {
					logrus.Info(text)
				} else {
					logrus.Error(text)
				}
			}

			if err := serverConn.WritePacket(pk); err != nil {
				if disconnect, ok := errors.Unwrap(err).(minecraft.DisconnectError); ok {
					_ = listener.Disconnect(conn, disconnect.Error())
				}
				return
			}
		}
	}()
	go func() {
		defer serverConn.Close()
		defer listener.Disconnect(conn, "connection lost")
		for {
			pk, err := serverConn.ReadPacket()
			if err != nil {
				if disconnect, ok := errors.Unwrap(err).(minecraft.DisconnectError); ok {
					_ = listener.Disconnect(conn, disconnect.Error())
				}
				return
			}

			pkText, err := packetToLog(pk, &hiddenReceivePacketsCountAtomic)
			if pkText != "" {
				text := receivePrefix + pkText
				if err == nil {
					logrus.Info(text)
				} else {
					logrus.Error(text)
				}
			}

			if err := conn.WritePacket(pk); err != nil {
				return
			}
		}
	}()
}

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
	FileWatcher struct {
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
	_ = readConfigNoWrite(configPath, &c)

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
		log.Fatalf("error reading config: %v", err)
	}
	if err := toml.Unmarshal(data, c); err != nil {
		log.Fatalf("error decoding config: %v", err)

		return err
	}

	return nil
}

func packetToLog(pk packet.Packet, countPointer *atomic.Int32) (text string, err error) {
	packetTypeName := fmt.Sprintf("%T", pk)
	c := configAtomic.Load()

	for _, ShowPacketType := range c.PacketLogger.ShowPacketType {
		if strings.Contains(packetTypeName, ShowPacketType) {
			const (
				prefix = "=========="
				suffix = " PACKET " + prefix
			)
			text += packetTypeName + "\n"

			if pkMarshal, err2 := toml.Marshal(pk); err != nil {
				err = err2
				text += err.Error()
			} else {
				text += prefix + " BEGIN " + suffix + "\n"
				text += string(pkMarshal)
				text += prefix + " END " + suffix
			}
			return
		}
	}
	countPointer.Add(1)

	return
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

func startRportingHiddenPacketCount(prefix string, newDelayChannel <-chan time.Duration, countPointer *atomic.Int32) {
	const template = "%d hidden packets."
	var (
		delay time.Duration
		t     *time.Ticker
	)
	defer func() {
		if t != nil {
			t.Stop()
		}
	}()

	for {
		if delay <= 0 {
			delay = <-newDelayChannel
			if t == nil {
				t = time.NewTicker(delay)
			} else {
				t.Reset(delay)
			}
		}

		count := countPointer.Load()
		countPointer.Store(0)
		if count > 0 {
			logrus.Infof(
				prefix+template,
				count,
			)
		}

		select {
		case <-t.C:
		case delay = <-newDelayChannel:
			t.Reset(delay)
		}
	}
}
