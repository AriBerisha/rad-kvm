package main

// Minimal RFC 6455 WebSocket server — just enough to read small text frames from
// one browser client (input events). No external dependency, so the binary
// builds with only the Go toolchain. Server->client is used only for ping/pong.

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

type wsConn struct {
	rw *bufio.ReadWriter
	c  io.Closer
}

func wsUpgrade(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	if r.Header.Get("Upgrade") != "websocket" {
		return nil, errors.New("not a websocket upgrade request")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, errors.New("missing Sec-WebSocket-Key")
	}
	sum := sha1.Sum([]byte(key + wsGUID))
	accept := base64.StdEncoding.EncodeToString(sum[:])

	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("response writer does not support hijacking")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := rw.WriteString(resp); err != nil {
		conn.Close()
		return nil, err
	}
	if err := rw.Flush(); err != nil {
		conn.Close()
		return nil, err
	}
	return &wsConn{rw: rw, c: conn}, nil
}

// ReadMessage returns the payload of the next text/binary frame. It answers
// pings, and returns io.EOF on a close frame. Input messages are tiny and never
// fragmented, so each data frame is treated as a complete message.
func (c *wsConn) ReadMessage() ([]byte, error) {
	for {
		var h [2]byte
		if _, err := io.ReadFull(c.rw, h[:]); err != nil {
			return nil, err
		}
		opcode := h[0] & 0x0f
		masked := h[1]&0x80 != 0
		n := int(h[1] & 0x7f)
		switch n {
		case 126:
			var ext [2]byte
			if _, err := io.ReadFull(c.rw, ext[:]); err != nil {
				return nil, err
			}
			n = int(binary.BigEndian.Uint16(ext[:]))
		case 127:
			var ext [8]byte
			if _, err := io.ReadFull(c.rw, ext[:]); err != nil {
				return nil, err
			}
			n = int(binary.BigEndian.Uint64(ext[:]))
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(c.rw, mask[:]); err != nil {
				return nil, err
			}
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(c.rw, payload); err != nil {
			return nil, err
		}
		if masked {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}
		switch opcode {
		case 0x1, 0x2: // text, binary
			return payload, nil
		case 0x8: // close
			return nil, io.EOF
		case 0x9: // ping -> pong
			_ = c.writeFrame(0xA, payload)
		case 0xA: // pong, ignore
		}
	}
}

func (c *wsConn) writeFrame(opcode byte, payload []byte) error {
	hdr := []byte{0x80 | opcode}
	n := len(payload)
	switch {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n < 65536:
		hdr = append(hdr, 126, byte(n>>8), byte(n))
	default:
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		hdr = append(hdr, 127)
		hdr = append(hdr, ext[:]...)
	}
	if _, err := c.rw.Write(hdr); err != nil {
		return err
	}
	if _, err := c.rw.Write(payload); err != nil {
		return err
	}
	return c.rw.Flush()
}

func (c *wsConn) Close() error { return c.c.Close() }
