package main

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

type WSConn struct {
	conn net.Conn
	r    *bufio.Reader
	mu   sync.Mutex
}

func UpgradeWebSocket(w http.ResponseWriter, r *http.Request) (*WSConn, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return nil, errors.New("missing websocket upgrade")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, errors.New("missing websocket key")
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("websocket hijacking is not supported")
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return nil, err
	}

	accept := websocketAccept(key)
	_, err = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n")
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		return nil, err
	}

	return &WSConn{conn: conn, r: rw.Reader}, nil
}

func websocketAccept(key string) string {
	sum := sha1.Sum([]byte(key + websocketGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func (c *WSConn) ReadText() ([]byte, error) {
	var message bytes.Buffer
	readingFragmentedMessage := false

	for {
		fin, opcode, payload, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		switch opcode {
		case 0x1:
			if fin {
				return payload, nil
			}
			message.Reset()
			message.Write(payload)
			readingFragmentedMessage = true
		case 0x0:
			if !readingFragmentedMessage {
				return nil, errors.New("unexpected continuation frame")
			}
			message.Write(payload)
			if fin {
				data := append([]byte(nil), message.Bytes()...)
				message.Reset()
				readingFragmentedMessage = false
				return data, nil
			}
		case 0x8:
			_ = c.Close()
			return nil, io.EOF
		case 0x9:
			_ = c.writeFrame(0xA, payload)
		default:
			continue
		}
	}
}

func (c *WSConn) WriteText(payload []byte) error {
	return c.writeFrame(0x1, payload)
}

func (c *WSConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Close()
}

func (c *WSConn) readFrame() (bool, byte, []byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(c.r, header); err != nil {
		return false, 0, nil, err
	}

	fin := header[0]&0x80 != 0
	opcode := header[0] & 0x0F
	masked := header[1]&0x80 != 0
	length := uint64(header[1] & 0x7F)
	if length == 126 {
		extended := make([]byte, 2)
		if _, err := io.ReadFull(c.r, extended); err != nil {
			return false, 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(extended))
	} else if length == 127 {
		extended := make([]byte, 8)
		if _, err := io.ReadFull(c.r, extended); err != nil {
			return false, 0, nil, err
		}
		length = binary.BigEndian.Uint64(extended)
	}
	const maxFrameSize = 16 << 20
	if length > maxFrameSize {
		return false, 0, nil, errors.New("websocket frame exceeds 16MiB")
	}

	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(c.r, mask[:]); err != nil {
			return false, 0, nil, err
		}
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(c.r, payload); err != nil {
		return false, 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return fin, opcode, payload, nil
}

func (c *WSConn) writeFrame(opcode byte, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	header := []byte{0x80 | opcode}
	length := len(payload)
	if length < 126 {
		header = append(header, byte(length))
	} else if length <= 65535 {
		header = append(header, 126, byte(length>>8), byte(length))
	} else {
		extended := make([]byte, 8)
		binary.BigEndian.PutUint64(extended, uint64(length))
		header = append(header, 127)
		header = append(header, extended...)
	}

	if _, err := c.conn.Write(header); err != nil {
		return err
	}
	_, err := c.conn.Write(payload)
	return err
}
