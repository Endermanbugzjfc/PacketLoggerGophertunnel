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

	config := readConfig()
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
		defer listener.Disconnect(conn, "connection lost")
		defer serverConn.Close()
		for {
			pk, err := conn.ReadPacket()
			if err != nil {
				return
			}

			pkText, err := packetToLog(pk, true)
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

			pkText, err := packetToLog(pk, false)
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

	reportHiddenPacketCount := func(
		delay time.Duration,
		countPointer *atomic.Int32,
		prefix string,
	) {
		if delay <= 0 {
			return
		}

		const template = "%d hidden packets."
		for {
			count := countPointer.Load()
			countPointer.Store(0)
			if count > 0 {
				logrus.Infof(
					prefix+template,
					count,
				)
			}

			time.Sleep(delay)
		}
	}

	delays := config.PacketLogger.ReportHiddenPacketCountDelay
	go reportHiddenPacketCount(delays.Receive, &hiddenReceivePacketsCountAtomic, receivePrefix)
	go reportHiddenPacketCount(delays.Send, &hiddenSendPacketsCountAtomic, sendPrefix)
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
}

func readConfig() config {
	c := config{}
	if _, err := os.Stat("config.toml"); os.IsNotExist(err) {
		f, err := os.Create("config.toml")
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
	data, err := ioutil.ReadFile("config.toml")
	if err != nil {
		log.Fatalf("error reading config: %v", err)
	}
	if err := toml.Unmarshal(data, &c); err != nil {
		log.Fatalf("error decoding config: %v", err)
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

	data, _ = toml.Marshal(c)
	if err := ioutil.WriteFile("config.toml", data, 0644); err != nil {
		log.Fatalf("error writing config file: %v", err)
	}
	return c
}

func packetToLog(pk packet.Packet, send bool) (text string, err error) {
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
	count := &hiddenReceivePacketsCountAtomic
	if send {
		count = &hiddenSendPacketsCountAtomic
	}
	count.Add(1)

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
			fmt.Println(dep.Version, packetTypeReferenceLink)
			return
		}
	}

	packetTypeReferenceLink = fmt.Sprintf(packetTypeReferenceLinkTemplate, "latest")
}
