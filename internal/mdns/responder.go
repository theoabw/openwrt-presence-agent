package mdns

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync/atomic"
	"time"
)

const (
	maxQuestionsPerQuery = 8
	maxResponseBytes     = 1400
	maxQueryBytes        = 1500
	readTimeout          = time.Second
)

var multicastAddress = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

// Config identifies the endpoint to advertise. The advertised address is
// derived from ListenAddress or, when that is a wildcard address, from the
// first IPv4 address of Interface.
type Config struct {
	AgentID       string
	Port          int
	ListenAddress string
	Interface     string
}

// Responder announces the agent once and answers matching PTR, SRV, TXT, and A
// queries until its context is canceled or Close is called.
type Responder struct {
	advertisement advertisement
	interfaceName string
	logger        *slog.Logger
	conn          *net.UDPConn
	closed        atomic.Bool
}

// New validates the advertised endpoint and binds the multicast socket. An
// ErrLoopbackOnly result means the caller must not advertise the API.
func New(c Config, logger *slog.Logger) (*Responder, error) {
	ad, err := newAdvertisement(c.AgentID, c.Port)
	if err != nil {
		return nil, err
	}
	address, err := advertisedAddress(c.ListenAddress, c.Interface)
	if err != nil {
		return nil, err
	}
	copy(ad.address[:], address.To4())
	return newResponder(ad, c.Interface, logger)
}

func newResponder(ad advertisement, interfaceName string, logger *slog.Logger) (*Responder, error) {
	if logger == nil {
		logger = slog.Default()
	}
	conn, err := openSocket(interfaceName)
	if err != nil {
		return nil, err
	}
	return &Responder{
		advertisement: ad, interfaceName: interfaceName, logger: logger, conn: conn,
	}, nil
}

func (r *Responder) Run(ctx context.Context) error {
	defer func() { _ = r.Close() }()
	r.logger.Info("mDNS advertisement started",
		"instance", r.advertisement.instance,
		"address", net.IP(r.advertisement.address[:]).String(),
		"port", r.advertisement.port,
		"interface", r.interfaceName,
	)
	if err := r.announce(announcementTTL); err != nil {
		if r.closed.Load() {
			return nil
		}
		return fmt.Errorf("announce mDNS service: %w", err)
	}
	nextRefresh := time.Now().Add(refreshInterval)
	buffer := make([]byte, maxQueryBytes)
	for ctx.Err() == nil {
		if !time.Now().Before(nextRefresh) {
			if err := r.announce(announcementTTL); err != nil {
				if r.closed.Load() {
					return nil
				}
				return fmt.Errorf("refresh mDNS announcement: %w", err)
			}
			nextRefresh = time.Now().Add(refreshInterval)
		}
		_ = r.conn.SetReadDeadline(time.Now().Add(readTimeout))
		count, _, err := r.conn.ReadFromUDP(buffer)
		if err != nil {
			if r.closed.Load() || errors.Is(err, net.ErrClosed) {
				return nil
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			return fmt.Errorf("read mDNS query: %w", err)
		}
		r.respond(buffer[:count])
	}
	return nil
}

// Close withdraws the advertisement and releases the socket. It is idempotent.
func (r *Responder) Close() error {
	if r.closed.Swap(true) {
		return nil
	}
	records, err := r.advertisement.records(0)
	if err != nil {
		_ = r.conn.Close()
		return fmt.Errorf("encode mDNS goodbye: %w", err)
	}
	sendErr := r.write(buildResponse(nil, records.all(), nil, maxResponseBytes))
	closeErr := r.conn.Close()
	if sendErr != nil {
		return fmt.Errorf("send mDNS goodbye: %w", sendErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close mDNS socket: %w", closeErr)
	}
	return nil
}

func (r *Responder) announce(ttl uint32) error {
	records, err := r.advertisement.records(ttl)
	if err != nil {
		return err
	}
	return r.write(buildResponse(nil, records.all(), nil, maxResponseBytes))
}

func (r *Responder) respond(packet []byte) {
	questions, err := parseQuery(packet, maxQuestionsPerQuery)
	if err != nil || len(questions) == 0 {
		return
	}
	response := r.advertisement.responseFor(questions, announcementTTL)
	if len(response.answers) == 0 {
		return
	}
	answer := buildResponse(response.questions, response.answers, response.additionals, maxResponseBytes)
	if err := r.write(answer); err != nil && !r.closed.Load() {
		r.logger.Debug("mDNS response failed", "error", err)
	}
}

func (r *Responder) write(packet []byte) error {
	if len(packet) > maxResponseBytes {
		return fmt.Errorf("mDNS packet exceeds %d bytes", maxResponseBytes)
	}
	_ = r.conn.SetWriteDeadline(time.Now().Add(time.Second))
	_, err := r.conn.WriteToUDP(packet, multicastAddress)
	return err
}
