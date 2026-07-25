package api

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/theoabw/openwrt-presence-agent/pkg/protocol"
)

const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func websocketUpgrade(w http.ResponseWriter, r *http.Request) (net.Conn, *bufio.ReadWriter, error) {
	if r.Method != http.MethodGet ||
		!headerContains(r.Header, "Connection", "upgrade") ||
		!strings.EqualFold(r.Header.Get("Upgrade"), "websocket") ||
		r.Header.Get("Sec-WebSocket-Version") != "13" {
		return nil, nil, fmt.Errorf("invalid WebSocket upgrade")
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(decoded) != 16 {
		return nil, nil, fmt.Errorf("invalid WebSocket key")
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("WebSocket unsupported")
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return nil, nil, err
	}
	digest := sha1.Sum([]byte(key + websocketGUID)) // RFC 6455 requires SHA-1 here.
	accept := base64.StdEncoding.EncodeToString(digest[:])
	_, err = fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)
	if err == nil {
		err = rw.Flush()
	}
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return conn, rw, nil
}

func headerContains(header http.Header, name, value string) bool {
	for _, line := range header.Values(name) {
		for _, part := range strings.Split(line, ",") {
			if strings.EqualFold(strings.TrimSpace(part), value) {
				return true
			}
		}
	}
	return false
}

func writeJSONFrame(w io.Writer, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeFrame(w, 0x1, payload)
}

func writeFrame(w io.Writer, opcode byte, payload []byte) error {
	header := []byte{0x80 | opcode}
	switch {
	case len(payload) <= 125:
		header = append(header, byte(len(payload)))
	case len(payload) <= 65535:
		header = append(header, 126, byte(len(payload)>>8), byte(len(payload)))
	default:
		var raw [8]byte
		binary.BigEndian.PutUint64(raw[:], uint64(len(payload)))
		header = append(header, 127)
		header = append(header, raw[:]...)
	}
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func monitorClientFrames(conn net.Conn, maxPayload int, done chan<- struct{}) {
	monitorClientFramesWithControl(conn, maxPayload, done, nil)
}

type clientControl struct {
	opcode  byte
	payload []byte
}

func monitorClientFramesWithControl(conn net.Conn, maxPayload int, done chan<- struct{}, controls chan<- clientControl) {
	defer close(done)
	for {
		var header [2]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			return
		}
		if header[0]&0x70 != 0 || header[0]&0x80 == 0 || header[1]&0x80 == 0 {
			return
		}
		opcode := header[0] & 0x0f
		length := uint64(header[1] & 0x7f)
		switch length {
		case 126:
			var raw [2]byte
			if _, err := io.ReadFull(conn, raw[:]); err != nil {
				return
			}
			length = uint64(binary.BigEndian.Uint16(raw[:]))
		case 127:
			var raw [8]byte
			if _, err := io.ReadFull(conn, raw[:]); err != nil {
				return
			}
			length = binary.BigEndian.Uint64(raw[:])
		}
		if length > uint64(maxPayload) {
			return
		}
		var mask [4]byte
		if _, err := io.ReadFull(conn, mask[:]); err != nil {
			return
		}
		payload := make([]byte, int(length))
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
		if opcode == 0x8 {
			return
		}
		if opcode == 0x9 && controls != nil {
			select {
			case controls <- clientControl{opcode: 0xA, payload: payload}:
			default:
				return
			}
		}
	}
}

func helloEvent(snapshot protocol.Snapshot) protocol.Event {
	return protocol.Event{
		Type: "stream.hello", EventID: snapshot.StreamEpoch + ":hello",
		StreamEpoch: snapshot.StreamEpoch, Sequence: snapshot.Sequence,
		Timestamp: time.Now().UTC(),
		Data:      map[string]any{"protocol_version": protocol.Version, "replay": false},
	}
}
