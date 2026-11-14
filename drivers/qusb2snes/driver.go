// Package qusb2snes is a driver for devices exposed by a usb2snes websocket server such as QUsb2Snes.
//
// It is not registered automatically; host applications that want it call DriverInit. (SNI itself implements the
// usb2snes protocol as a server, so it does not use this driver.)
package qusb2snes

import (
	"fmt"
	"log"
	"net/url"
	"sync"

	"github.com/alttpo/snes-devices/config"
	"github.com/alttpo/snes-devices/devices"
	"github.com/alttpo/snes-devices/internal/util"
	"github.com/alttpo/snes-devices/protos/sni"
)

const driverName = "qusb2snes"

const defaultHost = "localhost:8080"

var driver *Driver

type Driver struct {
	container devices.DeviceContainer

	// host:port of the usb2snes websocket server:
	host string

	// wsLock serializes commands across all websockets; the DeviceList opcode breaks other websockets.
	wsLock sync.Mutex

	detectedLock sync.Mutex
	detected     []devices.DeviceDescriptor
}

var driverCapabilities = []sni.DeviceCapability{
	sni.DeviceCapability_ReadMemory,
	sni.DeviceCapability_WriteMemory,
	sni.DeviceCapability_ResetSystem,
	sni.DeviceCapability_ResetToMenu,
}

func (d *Driver) DisplayOrder() int { return 3 }

func (d *Driver) DisplayName() string { return "QUsb2Snes" }

func (d *Driver) DisplayDescription() string { return "Connect to the QUsb2Snes service" }

func (d *Driver) Kind() string { return driverName }

func (d *Driver) HasCapabilities(capabilities ...sni.DeviceCapability) (bool, error) {
	return devices.CheckCapabilities(capabilities, driverCapabilities)
}

func (d *Driver) wsURL() string { return fmt.Sprintf("ws://%s/", d.host) }

func (d *Driver) Detect() (devs []devices.DeviceDescriptor, err error) {
	// prevent auto-detection while a device is open because the DeviceList opcode breaks other websockets:
	if len(d.container.AllDeviceKeys()) > 0 {
		d.detectedLock.Lock()
		devs = d.detected
		d.detectedLock.Unlock()
		return
	}

	var ws WebSocketClient
	err = NewWebSocketClient(&ws, d.wsURL(), RandomName("o2d"))
	defer func() {
		_ = ws.Close()
	}()
	if err != nil {
		// silence "connection refused" errors when the server is not running:
		if util.IsConnectionRefused(err) {
			err = nil
		}
		return
	}

	d.wsLock.Lock()
	defer d.wsLock.Unlock()

	err = ws.SendCommand(qusbCommand{
		Opcode:   "DeviceList",
		Space:    "SNES",
		Operands: []string{},
	})
	if err != nil {
		err = fmt.Errorf("qusb2snes: DeviceList request: %w", err)
		return
	}

	var list qusbResult
	err = ws.ReadCommandResponse("DeviceList", &list)
	if err != nil {
		return
	}

	devs = make([]devices.DeviceDescriptor, 0, len(list.Results))
	for _, name := range list.Results {
		devs = append(devs, devices.DeviceDescriptor{
			Uri:                 url.URL{Scheme: driverName, Host: d.host, RawQuery: url.Values{"device": {name}}.Encode()},
			DisplayName:         name,
			Kind:                driverName,
			Capabilities:        driverCapabilities[:],
			DefaultAddressSpace: sni.AddressSpace_FxPakPro,
			System:              "snes",
		})
	}

	d.detectedLock.Lock()
	d.detected = devs
	d.detectedLock.Unlock()
	return
}

func (d *Driver) DeviceKey(uri *url.URL) string {
	return uri.Host + "/" + uri.Query().Get("device")
}

func (d *Driver) Device(uri *url.URL) devices.AutoCloseableDevice {
	return devices.NewAutoCloseableDevice(d.container, uri, d.DeviceKey(uri))
}

func (d *Driver) openDevice(uri *url.URL) (devices.Device, error) {
	host := uri.Host
	if host == "" {
		host = d.host
	}

	dev := &Device{d: d, deviceName: uri.Query().Get("device")}
	if err := NewWebSocketClient(&dev.ws, fmt.Sprintf("ws://%s/", host), RandomName("o2")); err != nil {
		return nil, devices.DeviceFatal("qusb2snes: could not connect", err)
	}
	if err := dev.init(); err != nil {
		_ = dev.ws.Close()
		return nil, devices.DeviceFatal("qusb2snes: could not attach to device", err)
	}

	return dev, nil
}

func (d *Driver) DisconnectAll() {
	for _, deviceKey := range d.container.AllDeviceKeys() {
		device, ok := d.container.GetDevice(deviceKey)
		if ok {
			log.Printf("%s: disconnecting device '%s'\n", driverName, deviceKey)
			_ = device.Close()
			d.container.DeleteDevice(deviceKey)
		}
	}
}

// DriverInit registers the qusb2snes driver unless "qusb2snes_disable" is set. The websocket server address is read
// from "qusb2snes_host" (default localhost:8080).
func DriverInit() {
	cfg := config.Get()
	if cfg.GetBool("qusb2snes_disable") {
		log.Printf("disabling qusb2snes snes driver\n")
		return
	}

	host := cfg.GetString("qusb2snes_host")
	if host == "" {
		host = defaultHost
	}

	driver = &Driver{host: host}
	driver.container = devices.NewDeviceDriverContainer(driver.openDevice)
	devices.Register(driverName, driver)
}
