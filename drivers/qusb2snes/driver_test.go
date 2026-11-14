package qusb2snes

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"sync"
	"testing"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"

	"github.com/alttpo/snes-devices/devices"
	"github.com/alttpo/snes-devices/protos/sni"
)

// fakeServer is a minimal usb2snes websocket server.
type fakeServer struct {
	deviceName string

	lock       sync.Mutex
	memory     [0x1000000]byte
	getAddress int // number of GetAddress requests received
}

func (s *fakeServer) handle(conn net.Conn) {
	defer conn.Close()
	for {
		msg, op, err := wsutil.ReadClientData(conn)
		if err != nil {
			return
		}
		if op != ws.OpText {
			continue
		}
		var cmd qusbCommand
		if err = json.Unmarshal(msg, &cmd); err != nil {
			return
		}

		reply := func(results ...string) {
			b, _ := json.Marshal(qusbResult{Results: results})
			_ = wsutil.WriteServerText(conn, b)
		}

		switch cmd.Opcode {
		case "DeviceList":
			reply(s.deviceName)
		case "Info":
			reply("1.10.3", s.deviceName, "No Info")
		case "GetAddress":
			s.lock.Lock()
			s.getAddress++
			var data []byte
			for i := 0; i+1 < len(cmd.Operands); i += 2 {
				addr, _ := strconv.ParseUint(cmd.Operands[i], 16, 32)
				size, _ := strconv.ParseUint(cmd.Operands[i+1], 16, 32)
				data = append(data, s.memory[addr:addr+size]...)
			}
			s.lock.Unlock()
			// usb2snes servers split binary responses arbitrarily:
			for len(data) > 0 {
				n := len(data)
				if n > 3 {
					n = 3
				}
				_ = wsutil.WriteServerBinary(conn, data[:n])
				data = data[n:]
			}
		case "PutAddress":
			for i := 0; i+1 < len(cmd.Operands); i += 2 {
				addr, _ := strconv.ParseUint(cmd.Operands[i], 16, 32)
				data, _, err := wsutil.ReadClientData(conn)
				if err != nil {
					return
				}
				s.lock.Lock()
				copy(s.memory[addr:], data)
				s.lock.Unlock()
			}
		}
	}
}

func startFakeServer(t *testing.T, deviceName string) (*fakeServer, *Driver) {
	t.Helper()
	s := &fakeServer{deviceName: deviceName}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, _, err := ws.UpgradeHTTP(r, w)
		if err != nil {
			return
		}
		go s.handle(conn)
	})}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { _ = srv.Close() })

	d := &Driver{host: lis.Addr().String()}
	d.container = devices.NewDeviceDriverContainer(d.openDevice)
	return s, d
}

func TestDriver_ReadWrite(t *testing.T) {
	for _, tc := range []struct {
		deviceName   string
		getAddresses int // GetAddress requests expected for one 2-read MultiReadMemory
	}{
		{"SD2SNES", 1},
		{"RetroArch", 2},
	} {
		t.Run(tc.deviceName, func(t *testing.T) {
			s, d := startFakeServer(t, tc.deviceName)

			devs, err := d.Detect()
			if err != nil {
				t.Fatal(err)
			}
			if len(devs) != 1 || devs[0].DisplayName != tc.deviceName {
				t.Fatalf("unexpected devices: %+v", devs)
			}

			uri := devs[0].Uri
			dev := d.Device(&uri)
			ctx := context.Background()

			wram := devices.AddressTuple{Address: 0xF50010, AddressSpace: sni.AddressSpace_FxPakPro}
			sram := devices.AddressTuple{Address: 0xE00100, AddressSpace: sni.AddressSpace_FxPakPro}
			if _, err = dev.MultiWriteMemory(ctx,
				devices.MemoryWriteRequest{RequestAddress: wram, Data: []byte{1, 2, 3, 4, 5}},
				devices.MemoryWriteRequest{RequestAddress: sram, Data: []byte{9, 8}},
			); err != nil {
				t.Fatal(err)
			}

			before := s.getAddress
			rsps, err := dev.MultiReadMemory(ctx,
				devices.MemoryReadRequest{RequestAddress: wram, Size: 5},
				devices.MemoryReadRequest{RequestAddress: sram, Size: 2},
			)
			if err != nil {
				t.Fatal(err)
			}
			if string(rsps[0].Data) != "\x01\x02\x03\x04\x05" || string(rsps[1].Data) != "\x09\x08" {
				t.Errorf("unexpected read data: % x / % x", rsps[0].Data, rsps[1].Data)
			}
			if n := s.getAddress - before; n != tc.getAddresses {
				t.Errorf("expected %d GetAddress requests; got %d", tc.getAddresses, n)
			}

			// SNES A-bus addresses are translated to the FX Pak Pro address space:
			bus := devices.AddressTuple{Address: 0x7E0010, AddressSpace: sni.AddressSpace_SnesABus, MemoryMapping: sni.MemoryMapping_LoROM}
			rsps, err = dev.MultiReadMemory(ctx, devices.MemoryReadRequest{RequestAddress: bus, Size: 2})
			if err != nil {
				t.Fatal(err)
			}
			if rsps[0].DeviceAddress.Address != 0xF50010 || string(rsps[0].Data) != "\x01\x02" {
				t.Errorf("unexpected translated read: %+v", rsps[0])
			}

			// detection is suppressed while a device is open:
			devs, err = d.Detect()
			if err != nil || len(devs) != 1 {
				t.Errorf("expected cached detection while open; got %v, %v", devs, err)
			}

			if err = dev.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
