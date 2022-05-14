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
	packetTypeReferencePackage      = "github.com/sandertv/gophertunnel"
	packetTypeReferenceLinkTemplate = "(Look at https://pkg.go.dev/" + packetTypeReferencePackage + "@%s/minecraft/protocol/packet#pkg-index)"
)

var (
	packetTypeReferenceLink string

	showPacketType   []string
	showPacketTypeMu sync.RWMutex
)

// The following program implements a proxy that forwards players from one local address to a remote address.
func main() {
	findPacketTypeReferencePackageVersion()

	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})
	logrus.SetLevel(logrus.TraceLevel)

	const configPath = "config.toml"
	c := readConfig(configPath)
	token, err := auth.RequestLiveToken()
	if err != nil {
		panic(err)
	}
	src := auth.RefreshTokenSource(token)

	p, err := minecraft.NewForeignStatusProvider(c.Connection.RemoteAddress)
	if err != nil {
		panic(err)
	}
	listener, err := minecraft.ListenConfig{
		StatusProvider: p,
	}.Listen("raknet", c.Connection.LocalAddress)
	if err != nil {
		panic(err)
	}
	defer listener.Close()

	onReload := make([]func(c config), 3)
	onReload[0] = func(c config) {
		showPacketTypeMu.Lock()
		defer showPacketTypeMu.Unlock()

		showPacketType = c.PacketLogger.ShowPacketType
	}
	/*
		0 = receive.
		1 = send.
	*/
	loggerContexts := []loggerContext{
		{
			Prefix: "[Recieve] ",
		},
		{
			Prefix: "[Send] ",
		},
	}
	for index, context := range loggerContexts {
		newDelayChannel := make(chan time.Duration)
		context.CountHiddenDelayChannel = newDelayChannel
		context.CountHiddenAtomicPointer = &atomic.Int32{}
		loggerContexts[index] = context

		var f func(c config)
		switch index {
		default:
			f = func(config) {
				logrus.Debugf("Config update does not affect logger %s.", context.Prefix)
			}
		case 0:
			f = func(c config) {
				newDelayChannel <- c.PacketLogger.ReportHiddenPacketCountDelay.Receive
			}
		case 1:
			f = func(c config) {
				newDelayChannel <- c.PacketLogger.ReportHiddenPacketCountDelay.Send
			}
		}
		onReload[index+1] = f

		go startReportingHiddenPacketCount(context)
	}

	if c.Reload.ConfigAutoReload {
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
			go configAutoReload(configPath, watcher, onReload)
		}
	}
	// The original config.
	for _, f := range onReload {
		go f(c)
	}

	logrus.Info("Starting local proxy...")
	for {
		conn, err := listener.Accept()
		if err != nil {
			panic(err)
		}
		logrus.Info("New connection established.")
		// Address will not be affected by config reload.
		go handleConn(conn.(*minecraft.Conn), listener, c.Connection.RemoteAddress, src, loggerContexts)
	}
}

// configAutoReload runs each onReload function on its own goroutine.
func configAutoReload(configPath string, watcher *fsnotify.Watcher, onReload []func(c config)) {
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

			for _, f := range onReload {
				go f(c)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			logrus.Warnf("Failed to reload config: %s", err)
		}
	}
}

type loggerContext struct {
	Prefix                   string
	CountHiddenDelayChannel  <-chan time.Duration
	CountHiddenAtomicPointer *atomic.Int32
}

// handleConn handles a new incoming minecraft.Conn from the minecraft.Listener passed.
func handleConn(conn *minecraft.Conn, listener *minecraft.Listener, remoteAddress string, src oauth2.TokenSource, loggerContexts []loggerContext) {
	serverConn, err := minecraft.Dialer{
		TokenSource: src,
		ClientData:  conn.ClientData(),
	}.Dial("raknet", remoteAddress)
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

			context := loggerContexts[1]
			pkText, err := context.PacketToLog(pk)
			if pkText != "" {
				text := context.Prefix + pkText
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

			context := loggerContexts[0]
			pkText, err := context.PacketToLog(pk)
			if pkText != "" {
				text := context.Prefix + pkText
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

func (context loggerContext) PacketToLog(pk packet.Packet) (text string, err error) {
	packetTypeName := fmt.Sprintf("%T", pk)

	for _, ShowPacketType := range getShowPacketType() {
		if strings.Contains(packetTypeName, ShowPacketType) {
			const (
				prefix = "=========="
				suffix = " PACKET " + prefix
			)
			text += packetTypeName + "\n"

			if pkMarshal, err2 := toml.Marshal(pk); err2 != nil {
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
	context.CountHiddenAtomicPointer.Add(1)

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

func startReportingHiddenPacketCount(context loggerContext) {
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
		for delay <= 0 {
			delay = <-context.CountHiddenDelayChannel
		}
		if t == nil {
			t = time.NewTicker(delay)
		} else {
			t.Reset(delay)
		}

		counter := context.CountHiddenAtomicPointer
		count := counter.Load()
		counter.Store(0)
		if count > 0 {
			logrus.Infof(
				context.Prefix+template,
				count,
			)
		}

		select {
		case <-t.C:
		case delay = <-context.CountHiddenDelayChannel:
			if delay <= 0 {
				t.Stop()
				t = nil
			} else {
				t.Reset(delay)
			}
		}
	}
}

func getShowPacketType() []string {
	showPacketTypeMu.RLock()
	defer showPacketTypeMu.RUnlock()

	return showPacketType
}
