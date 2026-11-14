// Package qusb2snes is a driver for devices exposed by a usb2snes websocket server such as QUsb2Snes.
//
// It is not registered automatically; host applications that want it call DriverInit. (SNI itself implements the
// usb2snes protocol as a server, so it does not use this driver.)
package qusb2snes

import (
	"errors"
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

// host is the usb2snes websocket server address. QUsb2Snes (and SNI's usb2snes server) listen on port 23074; the
// legacy port 8080 is not used.
const host = "localhost:23074"

var driver *Driver

type Driver struct {
	container devices.DeviceContainer

	// host:port of the usb2snes websocket server:
	host string

	// lastDetectErr suppresses repeated logging of the same detection failure:
	lastDetectErr string

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

	// a server that is not running or not responding is not an error; there are just no devices. Detection is
	// polled frequently so failures are only logged when they change.
	var ws WebSocketClient
	err = NewWebSocketClient(&ws, d.wsURL(), RandomName("o2d"))
	defer func() {
		_ = ws.Close()
	}()
	if err != nil {
		if !util.IsConnectionRefused(err) {
			d.logDetectErr(err)
		}
		return nil, nil
	}

	d.wsLock.Lock()
	defer d.wsLock.Unlock()

	err = ws.SendCommand(qusbCommand{
		Opcode:   "DeviceList",
		Space:    "SNES",
		Operands: []string{},
	})
	if err != nil {
		d.logDetectErr(fmt.Errorf("DeviceList request: %w", err))
		return nil, nil
	}

	var list qusbResult
	err = ws.ReadCommandResponse("DeviceList", &list)
	if err != nil {
		d.logDetectErr(err)
		return nil, nil
	}
	d.logDetectOk()

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

// logDetectErr logs a detection failure unless it is the same as the previous one. Errors are compared by their
// root cause since the wrapping messages include the randomly generated client name.
func (d *Driver) logDetectErr(err error) {
	for errors.Unwrap(err) != nil {
		err = errors.Unwrap(err)
	}
	msg := err.Error()
	d.detectedLock.Lock()
	repeat := d.lastDetectErr == msg
	d.lastDetectErr = msg
	d.detectedLock.Unlock()
	if !repeat {
		log.Printf("qusb2snes: detect at %s: %v\n", d.host, err)
	}
}

// logDetectOk notes a successful detection so the next failure is logged again.
func (d *Driver) logDetectOk() {
	d.detectedLock.Lock()
	wasFailing := d.lastDetectErr != ""
	d.lastDetectErr = ""
	d.detectedLock.Unlock()
	if wasFailing {
		log.Printf("qusb2snes: detect at %s: server is reachable\n", d.host)
	}
}

func (d *Driver) DeviceKey(uri *url.URL) string {
	return uri.Host + "/" + uri.Query().Get("device")
}

func (d *Driver) Device(uri *url.URL) devices.AutoCloseableDevice {
	return devices.NewAutoCloseableDevice(d.container, uri, d.DeviceKey(uri))
}

func (d *Driver) openDevice(uri *url.URL) (devices.Device, error) {
	// always connect to the driver's server address, regardless of the host recorded in the URI:
	dev := &Device{d: d, deviceName: uri.Query().Get("device")}
	if err := NewWebSocketClient(&dev.ws, d.wsURL(), RandomName("o2")); err != nil {
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

// DriverInit registers the qusb2snes driver unless "qusb2snes_disable" is set. The driver connects to the usb2snes
// websocket server at localhost:23074.
func DriverInit() {
	if config.Get().GetBool("qusb2snes_disable") {
		log.Printf("disabling qusb2snes snes driver\n")
		return
	}

	driver = &Driver{host: host}
	driver.container = devices.NewDeviceDriverContainer(driver.openDevice)
	devices.Register(driverName, driver)
}
