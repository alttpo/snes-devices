package fxpakpro

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/alttpo/snes-devices/config"
	"github.com/alttpo/snes-devices/devices"
	"github.com/alttpo/snes-devices/protos/sni"
	"testing"
)

// testConfig is the configuration installed for this package's tests.
var testConfig = config.Map{}

func init() {
	// Host applications supply configuration through config.Set. Wire up just
	// the fxpakpro knobs from SNES_DEVICES_* environment variables here so
	// hardware tests can be run against different settings.
	config.Set(testConfig)
	for _, key := range []string{
		// "debug" enables the driver's per-chunk hex dumps. Wired up here so
		// its timing cost can be measured against the chunk-delay knob.
		"debug",
		"fxpakpro_read_timeout",
		"fxpakpro_write_timeout",
		"fxpakpro_honor_caller_deadline",
		"fxpakpro_chunk_delay",
	} {
		if v := os.Getenv("SNES_DEVICES_" + strings.ToUpper(key)); v != "" {
			testConfig[key] = v
		}
	}

	DriverInit()
}

func openAutoCloseableDevice(b testing.TB) devices.AutoCloseableDevice {
	var err error

	// detect fxpakpro devices connected to the system:
	var devs []devices.DeviceDescriptor
	devs, err = driver.Detect()
	if err != nil {
		b.Fatal(err)
	}

	if len(devs) == 0 {
		b.Skip("no fxpakpro devices found to test against")
	}

	uri := &devs[0].Uri

	var d devices.AutoCloseableDevice
	_, d, err = devices.DeviceByUri(uri)
	if err != nil {
		b.Fatal(err)
	}
	return d
}

func openExactDevice(tb testing.TB) *Device {
	var err error

	// detect fxpakpro devices connected to the system:
	var devs []devices.DeviceDescriptor
	devs, err = driver.Detect()
	if err != nil {
		tb.Fatal(err)
	}

	if len(devs) == 0 {
		tb.Skip("no fxpakpro devices found to test against")
	}

	uri := &devs[0].Uri

	var gendev devices.Device
	gendev, err = driver.openDevice(uri)
	if err != nil {
		tb.Fatal(err)
	}

	d := gendev.(*Device)

	return d
}

func BenchmarkMemory(b *testing.B) {
	var err error
	d := openAutoCloseableDevice(b)
	defer d.Close()

	// open the device with a single read:
	var rsp []devices.MemoryReadResponse
	rsp, err = d.MultiReadMemory(context.Background(), devices.MemoryReadRequest{
		RequestAddress: devices.AddressTuple{
			Address:       0xF50010,
			AddressSpace:  sni.AddressSpace_FxPakPro,
			MemoryMapping: sni.MemoryMapping_LoROM,
		},
		Size: 1,
	})
	_ = rsp
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.Run("WRAM read", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var rsp []devices.MemoryReadResponse
			rsp, err = d.MultiReadMemory(context.Background(), devices.MemoryReadRequest{
				RequestAddress: devices.AddressTuple{
					Address:       0xF50010,
					AddressSpace:  sni.AddressSpace_FxPakPro,
					MemoryMapping: sni.MemoryMapping_LoROM,
				},
				Size: 1,
			})
			_ = rsp
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("SRAM read", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var rsp []devices.MemoryReadResponse
			rsp, err = d.MultiReadMemory(context.Background(), devices.MemoryReadRequest{
				RequestAddress: devices.AddressTuple{
					Address:       0xE00000,
					AddressSpace:  sni.AddressSpace_FxPakPro,
					MemoryMapping: sni.MemoryMapping_LoROM,
				},
				Size: 1,
			})
			_ = rsp
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("SRAM write", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var rsp []devices.MemoryWriteResponse
			rsp, err = d.MultiWriteMemory(context.Background(), devices.MemoryWriteRequest{
				RequestAddress: devices.AddressTuple{
					Address:       0xE07FFF,
					AddressSpace:  sni.AddressSpace_FxPakPro,
					MemoryMapping: sni.MemoryMapping_LoROM,
				},
				Data: []byte{0xFF},
			})
			_ = rsp
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("WRAM write", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var rsp []devices.MemoryWriteResponse
			rsp, err = d.MultiWriteMemory(context.Background(), devices.MemoryWriteRequest{
				RequestAddress: devices.AddressTuple{
					Address:       0xF5F340,
					AddressSpace:  sni.AddressSpace_FxPakPro,
					MemoryMapping: sni.MemoryMapping_LoROM,
				},
				Data: []byte{0x04},
			})
			_ = rsp
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// firstDeviceURI returns the URI of the first detected fxpakpro device.
func firstDeviceURI() (*url.URL, error) {
	devs, err := driver.Detect()
	if err != nil {
		return nil, err
	}
	if len(devs) == 0 {
		return nil, fmt.Errorf("no fxpakpro devices found")
	}
	return &devs[0].Uri, nil
}
