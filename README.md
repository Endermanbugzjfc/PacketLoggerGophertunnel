# PacketLoggerGophertunnel
For Jibix.

# Configuration
## Default
```toml

[Connection]
  LocalAddress = "0.0.0.0:19132"
  RemoteAddress = ""

[PacketLogger]
  ShowPacketType = ["ActorEvent", "ActorPickRequest", "(Look at https://pkg.go.dev/github.com/sandertv/gophertunnel@v1.19.6/minecraft/protocol/packet#pkg-index)"]
```
## Show Packet Type
A whitelist of key phrases. If the fully qualified type name of a packet (`*packet.PacketTypeName`) contains any key phrases, its raw content will be visualised into TOML and then dumped to the console (if there is no error during the visualisation process).

I planned to upgrade this to expression-matching in the future.