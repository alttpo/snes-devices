// Package sniclient is a driver for devices exposed by a running SNI service over its gRPC API.
//
// It is not registered automatically; host applications that want it (e.g. o2) call DriverInit. SNI itself does not
// use this driver.
package sniclient

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/alttpo/snes-devices/config"
	"github.com/alttpo/snes-devices/devices"
	"github.com/alttpo/snes-devices/protos/sni"
)

const driverName = "sni"

const defaultAddr = "localhost:8191"

var driver *Driver

type Driver struct {
	container devices.DeviceContainer

	// addr is the host:port of the SNI gRPC service:
	addr string

	lock sync.Mutex
	cc   *grpc.ClientConn
}

func (d *Driver) DisplayOrder() int { return 2 }

func (d *Driver) DisplayName() string { return "SNI" }

func (d *Driver) DisplayDescription() string { return "Connect to SNI service" }

func (d *Driver) Kind() string { return driverName }

// driverCapabilities lists all capabilities a device behind SNI could support; actual capabilities are reported by
// SNI for each device in Detect.
var driverCapabilities = []sni.DeviceCapability{
	sni.DeviceCapability_ReadMemory,
	sni.DeviceCapability_WriteMemory,
	sni.DeviceCapability_ResetSystem,
	sni.DeviceCapability_ResetToMenu,
	sni.DeviceCapability_PauseUnpauseEmulation,
	sni.DeviceCapability_PauseToggleEmulation,
	sni.DeviceCapability_ReadDirectory,
	sni.DeviceCapability_MakeDirectory,
	sni.DeviceCapability_RemoveFile,
	sni.DeviceCapability_RenameFile,
	sni.DeviceCapability_PutFile,
	sni.DeviceCapability_GetFile,
	sni.DeviceCapability_BootFile,
}

func (d *Driver) HasCapabilities(capabilities ...sni.DeviceCapability) (bool, error) {
	return devices.CheckCapabilities(capabilities, driverCapabilities)
}

// conn returns the gRPC client connection to SNI shared by all devices.
func (d *Driver) conn() (*grpc.ClientConn, error) {
	d.lock.Lock()
	defer d.lock.Unlock()
	if d.cc == nil {
		cc, err := grpc.NewClient(d.addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, err
		}
		d.cc = cc
	}
	return d.cc, nil
}

func (d *Driver) Detect() (devs []devices.DeviceDescriptor, err error) {
	cc, err := d.conn()
	if err != nil {
		// SNI not reachable is not an error:
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rsp, err := sni.NewDevicesClient(cc).ListDevices(ctx, &sni.DevicesRequest{})
	if err != nil {
		// SNI not running is not an error:
		return nil, nil
	}

	devs = make([]devices.DeviceDescriptor, 0, len(rsp.GetDevices()))
	for _, dev := range rsp.GetDevices() {
		devs = append(devs, devices.DeviceDescriptor{
			Uri:                 url.URL{Scheme: driverName, Host: d.addr, RawQuery: url.Values{"uri": {dev.GetUri()}}.Encode()},
			DisplayName:         dev.GetDisplayName(),
			Kind:                dev.GetKind(),
			Capabilities:        dev.GetCapabilities(),
			DefaultAddressSpace: dev.GetDefaultAddressSpace(),
			System:              dev.GetSystem(),
		})
	}
	return
}

// DeviceKey is the URI of the device as known to SNI.
func (d *Driver) DeviceKey(uri *url.URL) string {
	return uri.Query().Get("uri")
}

func (d *Driver) Device(uri *url.URL) devices.AutoCloseableDevice {
	return devices.NewAutoCloseableDevice(d.container, uri, d.DeviceKey(uri))
}

func (d *Driver) openDevice(uri *url.URL) (devices.Device, error) {
	remoteURI := uri.Query().Get("uri")
	if remoteURI == "" {
		return nil, fmt.Errorf("sni: missing 'uri' query parameter in device URI")
	}

	cc, err := d.conn()
	if err != nil {
		return nil, devices.DeviceFatal(fmt.Sprintf("sni: could not connect to %s", d.addr), err)
	}

	return &Device{
		remoteURI:  remoteURI,
		memory:     sni.NewDeviceMemoryClient(cc),
		control:    sni.NewDeviceControlClient(cc),
		filesystem: sni.NewDeviceFilesystemClient(cc),
	}, nil
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

// DriverInit registers the SNI client driver unless "sni_disable" is set. The SNI gRPC address is read from
// "sni_grpc_addr" (default localhost:8191).
func DriverInit() {
	cfg := config.Get()
	if cfg.GetBool("sni_disable") {
		log.Printf("disabling sni snes driver\n")
		return
	}

	addr := cfg.GetString("sni_grpc_addr")
	if addr == "" {
		addr = defaultAddr
	}

	driver = &Driver{addr: addr}
	driver.container = devices.NewDeviceDriverContainer(driver.openDevice)
	devices.Register(driverName, driver)
}
