//go:build linux

package mdns

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

func TestResponderAnswersMulticastQuery(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ad := testAdvertisement(t)
	copy(ad.address[:], []byte{127, 0, 0, 1})
	responder, err := newResponder(ad, "lo", logger)
	if err != nil {
		t.Skipf("multicast sockets are unavailable: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- responder.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := responder.Close(); err != nil {
			t.Errorf("close responder: %v", err)
		}
		select {
		case err := <-runResult:
			if err != nil {
				t.Errorf("responder stopped: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("responder did not stop")
		}
	})

	queryConn, err := openSocket("lo")
	if err != nil {
		t.Skipf("multicast sockets are unavailable: %v", err)
	}
	defer queryConn.Close()
	query := buildTestQuery(t, serviceType, typePTR)
	if _, err := queryConn.WriteToUDP(query, multicastAddress); err != nil {
		t.Skipf("multicast send is unavailable: %v", err)
	}

	var (
		sawQuery bool
		response testMessage
	)
	buffer := make([]byte, 4096)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(response.answers) == 0 {
		_ = queryConn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
		count, _, err := queryConn.ReadFromUDP(buffer)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			break
		}
		message, err := parseTestMessage(buffer[:count])
		if err != nil {
			continue
		}
		if !message.response {
			sawQuery = true
			continue
		}
		if len(message.questions) == 1 &&
			strings.EqualFold(message.questions[0].name, serviceType) &&
			message.questions[0].qtype == typePTR {
			response = message
		}
	}
	if len(response.answers) == 0 {
		if sawQuery {
			t.Fatal("responder did not answer the PTR query")
		}
		t.Skip("multicast loopback is unavailable in this environment")
	}

	ptr, ok := findRecord(response.answers, serviceType, typePTR)
	if !ok {
		t.Fatalf("response has no PTR answer: %#v", response.answers)
	}
	if ptr.ttl != announcementTTL || ptr.class != classIN {
		t.Fatalf("PTR ttl/class = %d/%d", ptr.ttl, ptr.class)
	}
	instance, next, err := decodeName(ptr.data, 0)
	if err != nil || next != len(ptr.data) || instance != ad.instance {
		t.Fatalf("PTR target = %q, %v", instance, err)
	}
	srv, ok := findRecord(response.additionals, ad.instance, typeSRV)
	if !ok {
		t.Fatalf("response has no SRV additional record: %#v", response.additionals)
	}
	port, target := decodeSRV(t, srv.data)
	if port != ad.port || target != ad.host {
		t.Fatalf("SRV = port %d target %q", port, target)
	}
	txt, ok := findRecord(response.additionals, ad.instance, typeTXT)
	if !ok {
		t.Fatalf("response has no TXT additional record: %#v", response.additionals)
	}
	properties := decodeTXT(t, txt.data)
	if properties["agent_id"] != testAgentID || properties["tls"] != "false" {
		t.Fatalf("TXT properties = %#v", properties)
	}
	address, ok := findRecord(response.additionals, ad.host, typeA)
	if !ok {
		t.Fatalf("response has no A additional record: %#v", response.additionals)
	}
	if !bytes.Equal(address.data, ad.address[:]) {
		t.Fatalf("A record = %v", address.data)
	}
}
