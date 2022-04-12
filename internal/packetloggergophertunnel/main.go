package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/pelletier/go-toml"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/auth"
	"github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
)

func main() {
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})
	logrus.SetLevel(logrus.TraceLevel)

	conf := readConfig()
	logrus.Info("Requesting live token...")

	var tokenSource oauth2.TokenSource
	if token, err := auth.RequestLiveToken(); err != nil {
		panic(err)
	} else {
		logrus.Info("Refreshing token source...")
		tokenSource = auth.RefreshTokenSource(token)
	}

	logrus.Info("Dialing to remote address...")
	dialer := minecraft.Dialer{
		TokenSource: tokenSource,
	}
	address := conf.Connection.RemoteAddress

	if connection, err := dialer.Dial("raknet", address); err != nil {
		panic(err)
	} else {
		go func(connection *minecraft.Conn) {
			sigtermChannel := make(chan os.Signal, 2)
			signal.Notify(sigtermChannel, syscall.SIGINT, syscall.SIGTERM)
			<-sigtermChannel

			logrus.Info("Closing connection...")
			if err := connection.Close(); err != nil {
				logrus.Error(err)
			}
		}(connection)

		for {
			if packet, err := connection.ReadPacket(); err != nil {
				break
			} else {
				packetTypeName := fmt.Sprintf("%T\n", packet)
				if packetMarshal, err := toml.Marshal(packet); err != nil {
					logrus.Errorf(
						"%sFailed to print packet as TOML: %s",
						packetTypeName,
						err,
					)
				} else {
					logrus.Info(packetTypeName + string(packetMarshal))
				}
			}
		}
	}
}
