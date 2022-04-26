# PacketLoggerGophertunnel
For Jibix.

<details>
  <summary>Click To See Preview</summary>

```
Authentication successful.
INFO[2022-04-24T09:59:54+08:00] Creating file watcher...                     
INFO[2022-04-24T09:59:54+08:00] Adding config.toml to file watcher...        
INFO[2022-04-24T09:59:54+08:00] Starting local proxy...                      
INFO[2022-04-24T10:00:04+08:00] New connection established.                  
INFO[2022-04-24T10:00:11+08:00] [Recieve] *packet.LevelSoundEvent
========== BEGIN  PACKET ==========
BabyMob = false
DisableRelativeVolume = false
EntityType = "minecraft:player"
ExtraData = 3713
Position = [-632.106, 13.0, 122.5939]
SoundType = 35
========== END  PACKET ========== 
DEBU[2022-04-24T10:00:43+08:00] Reloaded config.                             
INFO[2022-04-24T10:00:43+08:00] [Recieve] 364 hidden packets.                
INFO[2022-04-24T10:00:43+08:00] [Send] 673 hidden packets.                   
INFO[2022-04-24T10:00:53+08:00] [Send] *packet.MovePlayer
========== BEGIN  PACKET ==========
EntityRuntimeID = 4146
HeadYaw = -162.5842
Mode = 0
OnGround = true
Pitch = 14.906464
Position = [-631.37463, 14.62001, 114.058914]
RiddenEntityRuntimeID = 0
TeleportCause = 0
TeleportSourceEntityType = 0
Tick = 0
Yaw = -162.5842
========== END  PACKET ========== 
INFO[2022-04-24T10:00:53+08:00] [Send] 201 hidden packets.                   
INFO[2022-04-24T10:00:53+08:00] [Recieve] 80 hidden packets.                 
INFO[2022-04-24T10:00:54+08:00] [Send] *packet.MovePlayer
========== BEGIN  PACKET ==========
EntityRuntimeID = 4146
HeadYaw = -162.5842
Mode = 0
OnGround = true
Pitch = 14.906464
Position = [-630.1268, 14.62001, 110.3]
RiddenEntityRuntimeID = 0
TeleportCause = 0
TeleportSourceEntityType = 0
Tick = 0
Yaw = -162.5842
========== END  PACKET ========== 
^C
```

</details>

# Configuration
## Default
Please create a `config.toml` in the working directory of an app instance.
```toml

[Connection]
  LocalAddress = "0.0.0.0:19132"
  RemoteAddress = ""

[PacketLogger]
  ShowPacketType = ["ActorEvent", "ActorPickRequest", "(Look at https://pkg.go.dev/github.com/sandertv/gophertunnel@v1.19.9/minecraft/protocol/packet#pkg-index)"]

  [PacketLogger.ReportHiddenPacketCountDelay]
    Receive = "5s"
    Send = "5s"

[Reload]
  ConfigAutoReload = false
```
## Show Packet Type
A whitelist of key phrases. If the fully qualified type name of a packet (`*packet.PacketTypeName`) contains any key phrases, its raw content will be visualised into TOML and then dumped to the console (if there is no error during the visualisation process).

I planned to upgrade this to expression-matching in the future.

## Report Hidden Packet Count Delay
The gap period between every hidden packet count report fire. The `s` in default duration stands for second. You wondering why? [https://www.techtarget.com/whatis/definition/second-s-or-sec](https://www.youtube.com/watch?v=dQw4w9WgXcQ)

Setting a report's delay to zero will disable that it.

## Config Auto Reload
Disabling this option when the app is running will turn off config auto reload until the current app instance (session) ends. In other words, until you restart it.

The connection section will not be affected by hot-reload or auto-reload.

# Start and End

I recommand you to run it from a terminal window rather than doubling clicking the executable file from your GUI file manager. This will allow you to read the logs and crash dump after the app exits.

Press Ctrl+C to exit the app. Beware that it will not disconnect your player properly. I were too lazy to code that (might implement in the future)...
