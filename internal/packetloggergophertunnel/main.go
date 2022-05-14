package main

import (
	"errors"
	"fmt"
	"runtime/debug"
	"sync"

	_ "github.com/icza/bitio"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/auth"
	"github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
)

const (
	packetTypeReferencePackage      = "github.com/sandertv/gophertunnel"
	packetTypeReferenceLinkTemplate = "(Look at https://pkg.go.dev/" + packetTypeReferencePackage + "@%s/minecraft/protocol/packet#pkg-index)"
)

var packetTypeReferenceLink string

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

	ctxs := makeLoggerContexts(c, configPath)

	logrus.Info("Starting local proxy...")
	for {
		conn, err := listener.Accept()
		if err != nil {
			panic(err)
		}
		logrus.Info("New connection established.")
		// Address will not be affected by config reload.
		go handleConn(conn.(*minecraft.Conn), listener, c.Connection.RemoteAddress, src, ctxs)
	}
}

// handleConn handles a new incoming minecraft.Conn from the minecraft.Listener passed.
func handleConn(conn *minecraft.Conn, listener *minecraft.Listener, remoteAddress string, src oauth2.TokenSource, ctxs loggerContexts) {
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
			ctxs[1].LogPacket(pk)

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
			ctxs[0].LogPacket(pk)

			if err := conn.WritePacket(pk); err != nil {
				return
			}
		}
	}()
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
