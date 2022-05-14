package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/pelletier/go-toml"
	"github.com/sandertv/gophertunnel/minecraft/packet"
	"github.com/sirupsen/logrus"
	"go.uber.org/atomic"
)

type (
	loggerContext struct {
		Prefix                   string
		CountHiddenDelayChannel  <-chan time.Duration
		CountHiddenAtomicPointer *atomic.Int32
	}
	loggerContexts [2]*loggerContext
)

func (context *loggerContext) LogPacket(pk packet.Packet) {
	var (
		text = context.Prefix
		err  error
	)
	defer func() {
		if err == nil {
			logrus.Info(text)
		} else {
			logrus.Error(text)
		}
	}()

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

func makeLoggerContexts(c config, configPath string) loggerContexts {
	ctxs := loggerContexts{
		{
			Prefix: "[Recieve] ",
		},
		{
			Prefix: "[Send] ",
		},
	}
	onReload := func(c config) {
		func() {
			showPacketTypeMu.Lock()
			defer showPacketTypeMu.Unlock()

			showPacketType = c.PacketLogger.ShowPacketType
		}()

		newDelayChannel := make(chan time.Duration)
		newDelayChannel <- c.PacketLogger.ReportHiddenPacketCountDelay.Receive
		for _, context := range ctxs {
			context.CountHiddenDelayChannel = newDelayChannel
			context.CountHiddenAtomicPointer = &atomic.Int32{}

			newDelayChannel <- c.PacketLogger.ReportHiddenPacketCountDelay.Send
			go startReportingHiddenPacketCount(context)
		}
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
	onReload(c)

	return ctxs
}

func getShowPacketType() []string {
	showPacketTypeMu.RLock()
	defer showPacketTypeMu.RUnlock()

	return showPacketType
}

func startReportingHiddenPacketCount(context *loggerContext) {
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
