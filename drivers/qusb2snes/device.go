package qusb2snes

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/gobwas/ws/wsutil"
	"google.golang.org/grpc/codes"

	"github.com/alttpo/snes-devices/devices"
	"github.com/alttpo/snes-devices/mapping"
	"github.com/alttpo/snes-devices/protos/sni"
)

type Info struct {
	FirmwareVersion string
	DeviceName      string
	ROM             string
}

// Device is a device attached through a usb2snes websocket server. usb2snes addresses are in the FX Pak Pro address
// space.
type Device struct {
	d *Driver

	lock       sync.Mutex
	deviceName string
	ws         WebSocketClient
	info       Info
}

func (dev *Device) init() (err error) {
	dev.d.wsLock.Lock()
	defer dev.d.wsLock.Unlock()

	err = dev.ws.SendCommand(qusbCommand{
		Opcode:   "Attach",
		Space:    "SNES",
		Operands: []string{dev.deviceName},
	})
	if err != nil {
		return
	}

	err = dev.ws.SendCommand(qusbCommand{
		Opcode:   "Info",
		Space:    "SNES",
		Operands: []string{},
	})
	if err != nil {
		return
	}

	var rsp qusbResult
	err = dev.ws.ReadCommandResponse("Info", &rsp)
	if err != nil {
		return
	}
	if len(rsp.Results) < 3 {
		return fmt.Errorf("qusb2snes: unexpected Info response %v", rsp.Results)
	}

	dev.info.FirmwareVersion = rsp.Results[0]
	dev.info.DeviceName = rsp.Results[1]
	dev.info.ROM = rsp.Results[2]
	log.Printf("qusb2snes: [%s] firmware version '%s', device '%s', rom '%s'\n",
		dev.ws.appName, dev.info.FirmwareVersion, dev.info.DeviceName, dev.info.ROM)

	return
}

func (dev *Device) IsClosed() bool {
	dev.lock.Lock()
	defer dev.lock.Unlock()
	return dev.ws.checkOpen() != nil
}

func (dev *Device) Close() error {
	dev.lock.Lock()
	defer dev.lock.Unlock()
	return dev.ws.Close()
}

// fatal wraps websocket and protocol errors; the websocket cannot be trusted afterwards.
func (dev *Device) fatal(err error) error {
	if err == nil {
		return nil
	}
	return devices.DeviceFatal(fmt.Sprintf("qusb2snes: %v", err), err)
}

func (dev *Device) RequiresMemoryMappingForAddressSpace(_ context.Context, addressSpace sni.AddressSpace) (bool, error) {
	return addressSpace == sni.AddressSpace_SnesABus, nil
}

func (dev *Device) RequiresMemoryMappingForAddress(_ context.Context, address devices.AddressTuple) (bool, error) {
	return address.AddressSpace == sni.AddressSpace_SnesABus, nil
}

func (dev *Device) MultiReadMemory(_ context.Context, reads ...devices.MemoryReadRequest) (rsps []devices.MemoryReadResponse, err error) {
	addrs := make([]uint32, len(reads))
	for i, read := range reads {
		if addrs[i], err = mapping.TranslateAddress(read.RequestAddress, sni.AddressSpace_FxPakPro); err != nil {
			return nil, devices.WithCode(codes.InvalidArgument, err)
		}
	}

	dev.lock.Lock()
	defer dev.lock.Unlock()
	dev.d.wsLock.Lock()
	defer dev.d.wsLock.Unlock()

	rsps = make([]devices.MemoryReadResponse, len(reads))
	setResponse := func(i int, data []byte) {
		rsps[i] = devices.MemoryReadResponse{
			RequestAddress: reads[i].RequestAddress,
			DeviceAddress: devices.AddressTuple{
				Address:       addrs[i],
				AddressSpace:  sni.AddressSpace_FxPakPro,
				MemoryMapping: reads[i].RequestAddress.MemoryMapping,
			},
			Data: data,
		}
	}

	// only the SD2SNES (FX Pak Pro) supports multiple addresses per GetAddress request:
	if dev.info.DeviceName == "SD2SNES" {
		operands := make([]string, 0, 2*len(reads))
		sumExpected := 0
		for i, read := range reads {
			operands = append(operands, fmt.Sprintf("%x", addrs[i]), fmt.Sprintf("%x", read.Size))
			sumExpected += read.Size
		}

		if err = dev.ws.SendCommand(qusbCommand{Opcode: "GetAddress", Space: "SNES", Operands: operands}); err != nil {
			return nil, dev.fatal(err)
		}

		var data []byte
		if data, err = dev.ws.ReadBinaryResponse(sumExpected); err != nil {
			return nil, dev.fatal(err)
		}

		n := 0
		for i, read := range reads {
			setResponse(i, data[n:n+read.Size])
			n += read.Size
		}
		return
	}

	for i, read := range reads {
		operands := []string{fmt.Sprintf("%x", addrs[i]), fmt.Sprintf("%x", read.Size)}
		if err = dev.ws.SendCommand(qusbCommand{Opcode: "GetAddress", Space: "SNES", Operands: operands}); err != nil {
			return nil, dev.fatal(err)
		}

		var data []byte
		if data, err = dev.ws.ReadBinaryResponse(read.Size); err != nil {
			return nil, dev.fatal(err)
		}
		setResponse(i, data)
	}

	return
}

func (dev *Device) MultiWriteMemory(_ context.Context, writes ...devices.MemoryWriteRequest) (rsps []devices.MemoryWriteResponse, err error) {
	addrs := make([]uint32, len(writes))
	for i, write := range writes {
		if addrs[i], err = mapping.TranslateAddress(write.RequestAddress, sni.AddressSpace_FxPakPro); err != nil {
			return nil, devices.WithCode(codes.InvalidArgument, err)
		}
	}

	dev.lock.Lock()
	defer dev.lock.Unlock()
	dev.d.wsLock.Lock()
	defer dev.d.wsLock.Unlock()

	operands := make([]string, 0, 2*len(writes))
	for i, write := range writes {
		operands = append(operands, fmt.Sprintf("%x", addrs[i]), fmt.Sprintf("%x", len(write.Data)))
	}
	if err = dev.ws.SendCommand(qusbCommand{Opcode: "PutAddress", Space: "SNES", Operands: operands}); err != nil {
		return nil, dev.fatal(err)
	}

	rsps = make([]devices.MemoryWriteResponse, len(writes))
	for i, write := range writes {
		if err = wsutil.WriteClientBinary(dev.ws.ws, write.Data); err != nil {
			return nil, dev.fatal(fmt.Errorf("writeClientBinary: %w", err))
		}
		rsps[i] = devices.MemoryWriteResponse{
			RequestAddress: write.RequestAddress,
			DeviceAddress: devices.AddressTuple{
				Address:       addrs[i],
				AddressSpace:  sni.AddressSpace_FxPakPro,
				MemoryMapping: write.RequestAddress.MemoryMapping,
			},
			Size: len(write.Data),
		}
	}

	return
}

func (dev *Device) sendControl(opcode string) error {
	dev.lock.Lock()
	defer dev.lock.Unlock()
	dev.d.wsLock.Lock()
	defer dev.d.wsLock.Unlock()

	return dev.fatal(dev.ws.SendCommand(qusbCommand{Opcode: opcode, Space: "SNES", Operands: []string{}}))
}

func (dev *Device) ResetSystem(_ context.Context) error { return dev.sendControl("Reset") }

func (dev *Device) ResetToMenu(_ context.Context) error { return dev.sendControl("Menu") }

func (dev *Device) PauseUnpause(_ context.Context, _ bool) (bool, error) {
	return false, devices.WithCode(codes.Unimplemented, fmt.Errorf("qusb2snes: pause not supported"))
}

func (dev *Device) PauseToggle(_ context.Context) error {
	return devices.WithCode(codes.Unimplemented, fmt.Errorf("qusb2snes: pause not supported"))
}
