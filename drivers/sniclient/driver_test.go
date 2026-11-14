package sniclient

import (
	"context"
	"errors"
	"net"
	"net/url"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/alttpo/snes-devices/devices"
	"github.com/alttpo/snes-devices/protos/sni"
)

// fakeSNI implements just enough of the SNI gRPC API to exercise the client driver.
type fakeSNI struct {
	sni.UnimplementedDevicesServer
	sni.UnimplementedDeviceMemoryServer

	memory  [0x1000000]byte
	readErr error
}

func (f *fakeSNI) ListDevices(context.Context, *sni.DevicesRequest) (*sni.DevicesResponse, error) {
	return &sni.DevicesResponse{Devices: []*sni.DevicesResponse_Device{{
		Uri:                 "fxpakpro://./dev/cu.usbmodemDEMO000000001",
		DisplayName:         "FX Pak Pro",
		Kind:                "fxpakpro",
		Capabilities:        []sni.DeviceCapability{sni.DeviceCapability_ReadMemory, sni.DeviceCapability_WriteMemory},
		DefaultAddressSpace: sni.AddressSpace_FxPakPro,
		System:              "snes",
	}}}, nil
}

func (f *fakeSNI) MultiRead(_ context.Context, req *sni.MultiReadMemoryRequest) (*sni.MultiReadMemoryResponse, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	rsp := &sni.MultiReadMemoryResponse{Uri: req.Uri}
	for _, r := range req.Requests {
		rsp.Responses = append(rsp.Responses, &sni.ReadMemoryResponse{
			RequestAddress:       r.RequestAddress,
			RequestAddressSpace:  r.RequestAddressSpace,
			RequestMemoryMapping: r.RequestMemoryMapping,
			DeviceAddress:        r.RequestAddress,
			DeviceAddressSpace:   r.RequestAddressSpace,
			Data:                 append([]byte(nil), f.memory[r.RequestAddress:r.RequestAddress+r.Size]...),
		})
	}
	return rsp, nil
}

func (f *fakeSNI) MultiWrite(_ context.Context, req *sni.MultiWriteMemoryRequest) (*sni.MultiWriteMemoryResponse, error) {
	rsp := &sni.MultiWriteMemoryResponse{Uri: req.Uri}
	for _, w := range req.Requests {
		copy(f.memory[w.RequestAddress:], w.Data)
		rsp.Responses = append(rsp.Responses, &sni.WriteMemoryResponse{
			RequestAddress:      w.RequestAddress,
			RequestAddressSpace: w.RequestAddressSpace,
			DeviceAddress:       w.RequestAddress,
			DeviceAddressSpace:  w.RequestAddressSpace,
			Size:                uint32(len(w.Data)),
		})
	}
	return rsp, nil
}

func startFakeSNI(t *testing.T) (*fakeSNI, *Driver) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSNI{}
	s := grpc.NewServer()
	sni.RegisterDevicesServer(s, f)
	sni.RegisterDeviceMemoryServer(s, f)
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)

	d := &Driver{addr: lis.Addr().String()}
	d.container = devices.NewDeviceDriverContainer(d.openDevice)
	return f, d
}

func TestDriver_DetectReadWrite(t *testing.T) {
	f, d := startFakeSNI(t)

	devs, err := d.Detect()
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 1 || devs[0].DisplayName != "FX Pak Pro" || devs[0].Uri.Scheme != driverName {
		t.Fatalf("unexpected devices: %+v", devs)
	}
	uri := devs[0].Uri
	if d.DeviceKey(&uri) != "fxpakpro://./dev/cu.usbmodemDEMO000000001" {
		t.Errorf("unexpected device key %q", d.DeviceKey(&uri))
	}

	dev := d.Device(&uri)
	ctx := context.Background()
	wram := devices.AddressTuple{Address: 0xF50010, AddressSpace: sni.AddressSpace_FxPakPro, MemoryMapping: sni.MemoryMapping_LoROM}
	if _, err = dev.MultiWriteMemory(ctx, devices.MemoryWriteRequest{RequestAddress: wram, Data: []byte{1, 2, 3}}); err != nil {
		t.Fatal(err)
	}
	if f.memory[0xF50011] != 2 {
		t.Errorf("write did not reach SNI")
	}

	rsps, err := dev.MultiReadMemory(ctx,
		devices.MemoryReadRequest{RequestAddress: wram, Size: 3},
		devices.MemoryReadRequest{RequestAddress: devices.AddressTuple{Address: 0xE00000, AddressSpace: sni.AddressSpace_FxPakPro}, Size: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(rsps) != 2 || string(rsps[0].Data) != "\x01\x02\x03" || len(rsps[1].Data) != 2 {
		t.Errorf("unexpected read responses: %+v", rsps)
	}
}

func TestDriver_ErrorClassification(t *testing.T) {
	f, d := startFakeSNI(t)
	uri := url.URL{Scheme: driverName, Host: d.addr, RawQuery: "uri=fxpakpro%3A%2F%2F.%2Fdev"}
	dev := d.Device(&uri)
	read := devices.MemoryReadRequest{RequestAddress: devices.AddressTuple{Address: 0xF50000, AddressSpace: sni.AddressSpace_FxPakPro}, Size: 1}

	for _, tc := range []struct {
		code  codes.Code
		fatal bool
	}{
		{codes.Internal, true},
		{codes.Unknown, true},
		{codes.DeadlineExceeded, false},
		{codes.InvalidArgument, false},
	} {
		f.readErr = status.Error(tc.code, "boom")
		_, err := dev.MultiReadMemory(context.Background(), read)
		if err == nil {
			t.Fatalf("%v: expected an error", tc.code)
		}
		if devices.IsFatal(err) != tc.fatal {
			t.Errorf("%v: fatal = %v; want %v", tc.code, devices.IsFatal(err), tc.fatal)
		}
		var derr devices.DeviceError
		if !errors.As(err, &derr) {
			t.Errorf("%v: expected a DeviceError", tc.code)
		}
	}
}
