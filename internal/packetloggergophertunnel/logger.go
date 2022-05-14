package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/df-mc/atomic"
	"github.com/fsnotify/fsnotify"
	"github.com/pelletier/go-toml"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
	"github.com/sirupsen/logrus"
)

type (
	loggerContext struct {
		Prefix         string
		ShowPacketType atomic.Value[[]string]

		// CountHidden:
		Delay <-chan time.Duration
		Count atomic.Int32
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
	for _, ShowPacketType := range context.ShowPacketType.Load() {
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
	context.Count.Add(1)

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

		newDelayChannel := make(chan time.Duration)
		newDelayChannel <- c.PacketLogger.ReportHiddenPacketCountDelay.Receive
		for _, context := range ctxs {
			context.ShowPacketType.Store(c.PacketLogger.ShowPacketType)
			go context.StartReportingHiddenPacketCount()
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

func (context loggerContext) StartReportingHiddenPacketCount() {
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
		for {
			delay = <-context.Delay

			if delay > 0 {
				t = time.NewTicker(delay)

				break
			}
		}

		count := context.Count.Load()
		context.Count.Store(0)
		if count > 0 {
			logrus.Infof(
				context.Prefix+template,
				count,
			)
		}

		select {
		case <-t.C:
		case delay = <-context.Delay:
			if delay <= 0 {
				t.Stop()
				t = nil
			} else {
				t.Reset(delay)
			}
		}
	}
}
