package sniclient

import (
	"context"
	"fmt"
	"io"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/alttpo/snes-devices/devices"
	"github.com/alttpo/snes-devices/protos/sni"
)

// Device forwards device operations to SNI. The gRPC connection is shared by all devices and owned by the Driver.
type Device struct {
	lock   sync.Mutex
	closed bool

	remoteURI  string
	memory     sni.DeviceMemoryClient
	control    sni.DeviceControlClient
	filesystem sni.DeviceFilesystemClient
}

func (d *Device) IsClosed() bool {
	d.lock.Lock()
	defer d.lock.Unlock()
	return d.closed
}

func (d *Device) Close() error {
	d.lock.Lock()
	defer d.lock.Unlock()
	d.closed = true
	return nil
}

// wrap classifies gRPC errors: Unknown and Internal errors from SNI mean the device is unusable; everything else
// (deadlines, unavailable service, bad requests) is non-fatal.
func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	if st, ok := status.FromError(err); ok && (st.Code() == codes.Unknown || st.Code() == codes.Internal) {
		return devices.DeviceFatal(fmt.Sprintf("sni: %s: %v", op, st.Message()), err)
	}
	return devices.DeviceNonFatal(fmt.Sprintf("sni: %s: %v", op, err), err)
}

func (d *Device) checkOpen() error {
	d.lock.Lock()
	defer d.lock.Unlock()
	if d.closed {
		return devices.DeviceFatal("sni: device is closed", nil)
	}
	return nil
}

func (d *Device) RequiresMemoryMappingForAddressSpace(_ context.Context, addressSpace sni.AddressSpace) (bool, error) {
	return addressSpace != sni.AddressSpace_FxPakPro && addressSpace != sni.AddressSpace_Raw, nil
}

func (d *Device) RequiresMemoryMappingForAddress(_ context.Context, address devices.AddressTuple) (bool, error) {
	return address.AddressSpace != sni.AddressSpace_FxPakPro && address.AddressSpace != sni.AddressSpace_Raw, nil
}

func (d *Device) MultiReadMemory(ctx context.Context, reads ...devices.MemoryReadRequest) ([]devices.MemoryReadResponse, error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}

	req := &sni.MultiReadMemoryRequest{Uri: d.remoteURI, Requests: make([]*sni.ReadMemoryRequest, len(reads))}
	for i, read := range reads {
		req.Requests[i] = &sni.ReadMemoryRequest{
			RequestAddress:       read.RequestAddress.Address,
			RequestAddressSpace:  read.RequestAddress.AddressSpace,
			RequestMemoryMapping: read.RequestAddress.MemoryMapping,
			Size:                 uint32(read.Size),
		}
	}

	rsp, err := d.memory.MultiRead(ctx, req)
	if err != nil {
		return nil, wrap("MultiRead", err)
	}
	if len(rsp.GetResponses()) != len(reads) {
		return nil, devices.DeviceNonFatal(fmt.Sprintf("sni: MultiRead: expected %d responses; got %d", len(reads), len(rsp.GetResponses())), nil)
	}

	rsps := make([]devices.MemoryReadResponse, len(reads))
	for i, r := range rsp.GetResponses() {
		if r.GetRequestAddress() != reads[i].RequestAddress.Address {
			return nil, devices.DeviceNonFatal("sni: MultiRead: mismatched address between request and response", nil)
		}
		rsps[i] = devices.MemoryReadResponse{
			RequestAddress: reads[i].RequestAddress,
			DeviceAddress: devices.AddressTuple{
				Address:       r.GetDeviceAddress(),
				AddressSpace:  r.GetDeviceAddressSpace(),
				MemoryMapping: r.GetRequestMemoryMapping(),
			},
			Data: r.GetData(),
		}
	}
	return rsps, nil
}

func (d *Device) MultiWriteMemory(ctx context.Context, writes ...devices.MemoryWriteRequest) ([]devices.MemoryWriteResponse, error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}

	req := &sni.MultiWriteMemoryRequest{Uri: d.remoteURI, Requests: make([]*sni.WriteMemoryRequest, len(writes))}
	for i, write := range writes {
		req.Requests[i] = &sni.WriteMemoryRequest{
			RequestAddress:       write.RequestAddress.Address,
			RequestAddressSpace:  write.RequestAddress.AddressSpace,
			RequestMemoryMapping: write.RequestAddress.MemoryMapping,
			Data:                 write.Data,
		}
	}

	rsp, err := d.memory.MultiWrite(ctx, req)
	if err != nil {
		return nil, wrap("MultiWrite", err)
	}
	if len(rsp.GetResponses()) != len(writes) {
		return nil, devices.DeviceNonFatal(fmt.Sprintf("sni: MultiWrite: expected %d responses; got %d", len(writes), len(rsp.GetResponses())), nil)
	}

	rsps := make([]devices.MemoryWriteResponse, len(writes))
	for i, r := range rsp.GetResponses() {
		if r.GetRequestAddress() != writes[i].RequestAddress.Address {
			return nil, devices.DeviceNonFatal("sni: MultiWrite: mismatched address between request and response", nil)
		}
		rsps[i] = devices.MemoryWriteResponse{
			RequestAddress: writes[i].RequestAddress,
			DeviceAddress: devices.AddressTuple{
				Address:       r.GetDeviceAddress(),
				AddressSpace:  r.GetDeviceAddressSpace(),
				MemoryMapping: r.GetRequestMemoryMapping(),
			},
			Size: int(r.GetSize()),
		}
	}
	return rsps, nil
}

func (d *Device) ResetSystem(ctx context.Context) error {
	if err := d.checkOpen(); err != nil {
		return err
	}
	_, err := d.control.ResetSystem(ctx, &sni.ResetSystemRequest{Uri: d.remoteURI})
	return wrap("ResetSystem", err)
}

func (d *Device) ResetToMenu(ctx context.Context) error {
	if err := d.checkOpen(); err != nil {
		return err
	}
	_, err := d.control.ResetToMenu(ctx, &sni.ResetToMenuRequest{Uri: d.remoteURI})
	return wrap("ResetToMenu", err)
}

func (d *Device) PauseUnpause(ctx context.Context, pausedState bool) (bool, error) {
	if err := d.checkOpen(); err != nil {
		return false, err
	}
	rsp, err := d.control.PauseUnpauseEmulation(ctx, &sni.PauseEmulationRequest{Uri: d.remoteURI, Paused: pausedState})
	if err != nil {
		return false, wrap("PauseUnpauseEmulation", err)
	}
	return rsp.GetPaused(), nil
}

func (d *Device) PauseToggle(ctx context.Context) error {
	if err := d.checkOpen(); err != nil {
		return err
	}
	_, err := d.control.PauseToggleEmulation(ctx, &sni.PauseToggleEmulationRequest{Uri: d.remoteURI})
	return wrap("PauseToggleEmulation", err)
}

func (d *Device) ReadDirectory(ctx context.Context, path string) ([]devices.DirEntry, error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	rsp, err := d.filesystem.ReadDirectory(ctx, &sni.ReadDirectoryRequest{Uri: d.remoteURI, Path: path})
	if err != nil {
		return nil, wrap("ReadDirectory", err)
	}
	entries := make([]devices.DirEntry, 0, len(rsp.GetEntries()))
	for _, e := range rsp.GetEntries() {
		entries = append(entries, devices.DirEntry{Name: e.GetName(), Type: e.GetType()})
	}
	return entries, nil
}

func (d *Device) MakeDirectory(ctx context.Context, path string) error {
	if err := d.checkOpen(); err != nil {
		return err
	}
	_, err := d.filesystem.MakeDirectory(ctx, &sni.MakeDirectoryRequest{Uri: d.remoteURI, Path: path})
	return wrap("MakeDirectory", err)
}

func (d *Device) RemoveFile(ctx context.Context, path string) error {
	if err := d.checkOpen(); err != nil {
		return err
	}
	_, err := d.filesystem.RemoveFile(ctx, &sni.RemoveFileRequest{Uri: d.remoteURI, Path: path})
	return wrap("RemoveFile", err)
}

func (d *Device) RenameFile(ctx context.Context, path, newFilename string) error {
	if err := d.checkOpen(); err != nil {
		return err
	}
	_, err := d.filesystem.RenameFile(ctx, &sni.RenameFileRequest{Uri: d.remoteURI, Path: path, NewFilename: newFilename})
	return wrap("RenameFile", err)
}

func (d *Device) PutFile(ctx context.Context, path string, size uint32, r io.Reader, progress devices.ProgressReportFunc) (uint32, error) {
	if err := d.checkOpen(); err != nil {
		return 0, err
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(r, data); err != nil {
		return 0, err
	}
	rsp, err := d.filesystem.PutFile(ctx, &sni.PutFileRequest{Uri: d.remoteURI, Path: path, Data: data})
	if err != nil {
		return 0, wrap("PutFile", err)
	}
	if progress != nil {
		progress(rsp.GetSize(), size)
	}
	return rsp.GetSize(), nil
}

func (d *Device) GetFile(ctx context.Context, path string, w io.Writer, sizeReceived devices.SizeReceivedFunc, progress devices.ProgressReportFunc) (uint32, error) {
	if err := d.checkOpen(); err != nil {
		return 0, err
	}
	rsp, err := d.filesystem.GetFile(ctx, &sni.GetFileRequest{Uri: d.remoteURI, Path: path})
	if err != nil {
		return 0, wrap("GetFile", err)
	}
	if sizeReceived != nil {
		sizeReceived(rsp.GetSize())
	}
	n, err := w.Write(rsp.GetData())
	if progress != nil {
		progress(uint32(n), rsp.GetSize())
	}
	return uint32(n), err
}

func (d *Device) BootFile(ctx context.Context, path string) error {
	if err := d.checkOpen(); err != nil {
		return err
	}
	_, err := d.filesystem.BootFile(ctx, &sni.BootFileRequest{Uri: d.remoteURI, Path: path})
	return wrap("BootFile", err)
}
