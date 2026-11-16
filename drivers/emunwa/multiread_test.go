package emunwa

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/alttpo/snes-devices/devices"
	"github.com/alttpo/snes-devices/protos/sni"
)

// TestMultiReadMemory_ErrorRepliesDrained checks that a read batch whose replies are errors (e.g. while the emulator
// has no game loaded) still consumes one reply per command, so that the next batch gets its own replies.
func TestMultiReadMemory_ErrorRepliesDrained(t *testing.T) {
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	gameLoaded := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		loaded := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if !loaded {
				select {
				case <-gameLoaded:
					loaded = true
				default:
				}
			}
			if !loaded {
				// reply separately, as an emulator often does, so a reply isn't buffered with the one before it:
				time.Sleep(20 * time.Millisecond)
				_, _ = conn.Write([]byte("\nerror:not_allowed\nreason:No game is currently loaded\n\n"))
				continue
			}
			// reply with each region's size in bytes, filled with the memory type's first letter:
			parts := strings.Split(strings.TrimSpace(line), ";")
			fill := strings.TrimPrefix(parts[0], "CORE_READ ")[0]
			var data []byte
			for i := 2; i < len(parts); i += 2 {
				var n uint32
				for _, c := range strings.TrimPrefix(parts[i], "$") {
					n = n*16 + uint32(strings.IndexRune("0123456789abcdef", c))
				}
				data = append(data, bytes.Repeat([]byte{fill}, int(n))...)
			}
			b := &bytes.Buffer{}
			b.WriteByte(0)
			_ = binary.Write(b, binary.BigEndian, uint32(len(data)))
			b.Write(data)
			_, _ = conn.Write(b.Bytes())
		}
	}()

	c := NewClient(ln.Addr().(*net.TCPAddr), "test", time.Second)
	c.MuteLog(true)
	if err = c.Connect(); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	addr := func(a uint32) devices.AddressTuple {
		return devices.AddressTuple{Address: a, AddressSpace: sni.AddressSpace_FxPakPro, MemoryMapping: sni.MemoryMapping_LoROM}
	}
	// a WRAM read and an SRAM read make two commands, and so two replies:
	reads := []devices.MemoryReadRequest{
		{RequestAddress: addr(0xF50010), Size: 4},
		{RequestAddress: addr(0xE07FF0), Size: 2},
	}

	for i := 0; i < 3; i++ {
		if _, err = c.MultiReadMemory(context.Background(), reads...); err == nil {
			t.Fatal("expected an error while no game is loaded")
		}
	}

	close(gameLoaded)
	rsps, err := c.MultiReadMemory(context.Background(), reads...)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(rsps[0].Data); got != "WWWW" {
		t.Errorf("WRAM read = %q; expected %q", got, "WWWW")
	}
	if got := string(rsps[1].Data); got != "SS" {
		t.Errorf("SRAM read = %q; expected %q", got, "SS")
	}
}
