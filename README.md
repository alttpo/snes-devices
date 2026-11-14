# snes-devices

SNES device drivers shared by [SNI](https://github.com/alttpo/sni) and [o2](https://github.com/alttpo/o2).

## Packages

| Package | Purpose |
|---|---|
| `devices` | Device and driver interfaces, the driver registry, device containers and auto-reopening devices |
| `mapping` | ROM memory mapping detection and address translation between address spaces |
| `config` | The `Config` interface drivers read settings through |
| `protos/sni` | Generated Go code for SNI's gRPC API (`sni.proto`) |
| `drivers/fxpakpro` | FX Pak Pro / SD2SNES over USB serial |
| `drivers/retroarch` | RetroArch network commands (registered as `ra`) |
| `drivers/emunwa` | Emulators implementing the Emulator Network Access protocol |
| `drivers/luabridge` | Emulators running SNI's Lua bridge script |
| `drivers/mock` | A mock device for testing |
| `drivers/qusb2snes` | Devices behind a usb2snes websocket server such as QUsb2Snes |
| `drivers/sniclient` | Devices behind a running SNI service, over its gRPC API |

## Using the drivers

Drivers are not registered automatically. A host application:

1. installs its configuration with `config.Set(...)`;
2. calls `DriverInit()` for each driver it wants (each driver may decline to register based on its configuration);
3. lists drivers with `devices.Drivers()`, detects devices with `Driver.Detect()` and opens one with
   `Driver.Device(uri)`.

### Configuration

Drivers read their settings through `config.Get()`. Keys are lowercase without any application prefix; each host
maps them to its own sources (for example SNI reads `SNI_*` environment variables and its config file, o2 reads
`O2_*` environment variables). `config.Config.Name(key)` gives the user-facing name of a setting for log messages.

| Key | Driver | Meaning |
|---|---|---|
| `debug` | all | verbose device logging |
| `verbose_logging`, `log_responses` | retroarch, emunwa, luabridge | protocol logging |
| `fxpakpro_disable` | fxpakpro | do not register |
| `fxpakpro_read_timeout`, `fxpakpro_write_timeout` | fxpakpro | I/O timeouts (durations) |
| `fxpakpro_honor_caller_deadline`, `fxpakpro_chunk_delay` | fxpakpro | see `drivers/fxpakpro/driver.go` |
| `retroarch_disable` | retroarch | do not register |
| `retroarch_hosts` | retroarch | comma-separated `host:port` list to probe |
| `retroarch_detect_log` | retroarch | log detection attempts |
| `emunw_disable` | emunwa | do not register |
| `emunw_hosts` | emunwa | comma-separated `host:port` list; computed from the port range when empty |
| `nwa_port_range` | emunwa | first port of the NWA port range (default `0xbeef`) |
| `nwa_disable_old_range` | emunwa | do not probe the legacy ports 65400-65409 |
| `emunw_detect_log` | emunwa | log detection attempts |
| `luabridge_listen_host`, `luabridge_listen_port` | luabridge | address to listen on for Lua scripts |
| `mock_enable` | mock | register the mock driver |
| `qusb2snes_disable` | qusb2snes | do not register |
| `qusb2snes_host` | qusb2snes | websocket server `host:port` (default `localhost:8080`) |
| `sni_disable` | sniclient | do not register |
| `sni_grpc_addr` | sniclient | SNI gRPC `host:port` (default `localhost:8191`) |

## Protobuf

`protos/sni` holds the generated Go code for `sni.proto`; it lives here so that SNI's server and the `sniclient`
driver link a single registration of the file. The canonical `sni.proto` for other languages remains in the SNI
repository; keep the two copies in sync.
