package main

// Wake-on-LAN: send a magic packet (6×0xFF + 16×MAC) as a UDP broadcast to
// port 9. Needs SO_BROADCAST on the socket, set via the dialer's Control hook
// (stdlib syscall, no external dep).

import (
	"errors"
	"net"
	"syscall"
)

func SendWoL(mac, broadcast string) error {
	hw, err := net.ParseMAC(mac)
	if err != nil || len(hw) != 6 {
		return errors.New("invalid MAC address")
	}
	if broadcast == "" {
		broadcast = "255.255.255.255"
	}

	pkt := make([]byte, 0, 102)
	for i := 0; i < 6; i++ {
		pkt = append(pkt, 0xff)
	}
	for i := 0; i < 16; i++ {
		pkt = append(pkt, hw...)
	}

	d := net.Dialer{Control: func(_, _ string, c syscall.RawConn) error {
		var serr error
		if cerr := c.Control(func(fd uintptr) {
			serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
		}); cerr != nil {
			return cerr
		}
		return serr
	}}

	conn, err := d.Dial("udp", net.JoinHostPort(broadcast, "9"))
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write(pkt)
	return err
}
