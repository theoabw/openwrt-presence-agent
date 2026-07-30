package api

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/theoabw/openwrt-presence-agent/pkg/protocol"
)

type readTrackingConn struct {
	net.Conn
	readStarted chan struct{}
	deadlineSet chan struct{}
}

func (c *readTrackingConn) Read(payload []byte) (int, error) {
	select {
	case <-c.readStarted:
	default:
		close(c.readStarted)
	}
	return c.Conn.Read(payload)
}

func (c *readTrackingConn) SetReadDeadline(deadline time.Time) error {
	select {
	case c.deadlineSet <- struct{}{}:
	default:
	}
	return c.Conn.SetReadDeadline(deadline)
}

func TestWriteFrameUses64BitLength(t *testing.T) {
	payload := make([]byte, 65536)
	var buffer bytes.Buffer
	if err := writeFrame(&buffer, 1, payload); err != nil {
		t.Fatal(err)
	}
	data := buffer.Bytes()
	if data[1] != 127 || binary.BigEndian.Uint64(data[2:10]) != uint64(len(payload)) {
		t.Fatalf("invalid frame header %x", data[:10])
	}
}

func TestPassiveWebSocketClientDoesNotRequireInboundTraffic(t *testing.T) {
	server, client := net.Pipe()
	tracked := &readTrackingConn{
		Conn: server, readStarted: make(chan struct{}), deadlineSet: make(chan struct{}, 1),
	}
	done := make(chan struct{})
	go monitorClientFrames(tracked, 1024, done)

	select {
	case <-tracked.readStarted:
	case <-time.After(time.Second):
		t.Fatal("frame monitor did not begin reading")
	}
	select {
	case <-tracked.deadlineSet:
		t.Fatal("frame monitor imposed an inbound-traffic deadline")
	default:
	}
	select {
	case <-done:
		t.Fatal("passive client connection was closed")
	default:
	}

	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("frame monitor did not stop after connection closure")
	}
}

func TestWebSocketStartsWithSnapshotThenOrderedEvents(t *testing.T) {
	server, state := testServer(t)
	state.SetProvider(protocol.Provider{
		ID: "ubus-hostapd", Kind: "wifi", Status: "healthy",
		LastSnapshotAt: time.Now().UTC(),
	})
	httpServer := http.Server{Handler: server.http.Handler}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() { _ = httpServer.Serve(listener) }()
	defer httpServer.Close()

	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	request := fmt.Sprintf(
		"GET /v1/events HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: AAECAwQFBgcICQoLDA0ODw==\r\n\r\n",
		listener.Addr().String(), testToken,
	)
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet, URL: &url.URL{Path: "/v1/events"}})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status = %d", response.StatusCode)
	}
	hello := readServerEvent(t, reader)
	snapshot := readServerEvent(t, reader)
	if hello.Type != "stream.hello" || snapshot.Type != "state.snapshot" {
		t.Fatalf("initial events = %q, %q", hello.Type, snapshot.Type)
	}
	if err := state.Associate("ubus", "hostapd.wlan0", "mac:00:11:22:33:44:55", time.Now().UTC(), "test"); err != nil {
		t.Fatal(err)
	}
	event := readServerEvent(t, reader)
	if event.Sequence != snapshot.Sequence+1 || event.Type != "client.presence_changed" {
		t.Fatalf("first state event = %#v", event)
	}
}

func readServerEvent(t *testing.T, reader *bufio.Reader) protocol.Event {
	t.Helper()
	first, err := reader.ReadByte()
	if err != nil {
		t.Fatal(err)
	}
	second, err := reader.ReadByte()
	if err != nil {
		t.Fatal(err)
	}
	if first&0x0f != 1 || second&0x80 != 0 {
		t.Fatalf("unexpected frame header %02x %02x", first, second)
	}
	length := uint64(second & 0x7f)
	switch length {
	case 126:
		var raw [2]byte
		if _, err := io.ReadFull(reader, raw[:]); err != nil {
			t.Fatal(err)
		}
		length = uint64(binary.BigEndian.Uint16(raw[:]))
	case 127:
		var raw [8]byte
		if _, err := io.ReadFull(reader, raw[:]); err != nil {
			t.Fatal(err)
		}
		length = binary.BigEndian.Uint64(raw[:])
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		t.Fatal(err)
	}
	var event protocol.Event
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatal(err)
	}
	return event
}
