package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/theoabw/openwrt-presence-agent/internal/auth"
	"github.com/theoabw/openwrt-presence-agent/internal/config"
	"github.com/theoabw/openwrt-presence-agent/internal/engine"
	"github.com/theoabw/openwrt-presence-agent/pkg/protocol"
)

type Server struct {
	config    config.Config
	engine    *engine.Engine
	auth      *auth.Bearer
	agentID   string
	version   string
	startedAt time.Time
	logger    *slog.Logger
	http      *http.Server
	shutdown  chan struct{}
	stopOnce  sync.Once
}

func New(c config.Config, e *engine.Engine, bearer *auth.Bearer, agentID, version string, logger *slog.Logger) *Server {
	s := &Server{
		config: c, engine: e, auth: bearer, agentID: agentID, version: version,
		startedAt: time.Now().UTC(), logger: logger, shutdown: make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/info", s.info)
	mux.HandleFunc("GET /v1/health", s.health)
	mux.HandleFunc("GET /v1/clients", s.clients)
	mux.HandleFunc("GET /v1/clients/{id}", s.client)
	mux.HandleFunc("GET /v1/providers", s.providers)
	mux.HandleFunc("GET /v1/diagnostics", s.diagnostics)
	mux.HandleFunc("GET /v1/events", s.events)
	s.http = &http.Server{
		Addr: c.Address(), Handler: bearer.Middleware(mux),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: 16 * 1024,
	}
	return s
}

func (s *Server) ListenAndServe() error {
	listener, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return err
	}
	err = s.http.Serve(newLimitedListener(listener, s.config.MaxHTTPConnections))
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.stopOnce.Do(func() { close(s.shutdown) })
	return s.http.Shutdown(ctx)
}

func (s *Server) info(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name": "openwrt-presence-agent", "protocol_version": protocol.Version,
		"agent_id": s.agentID, "version": s.version,
		"capabilities": []string{"wifi_snapshot", "wifi_events", "websocket"},
	})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	status := "not_ready"
	code := http.StatusServiceUnavailable
	for _, provider := range s.engine.Providers() {
		if provider.Status == "healthy" {
			status = "operational"
			code = http.StatusOK
			break
		}
		if provider.Status == "unavailable" && !provider.LastSnapshotAt.IsZero() {
			status = "degraded"
			code = http.StatusOK
		}
	}
	writeJSON(w, code, map[string]any{"status": status, "providers": s.engine.Providers()})
}

func (s *Server) clients(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.Snapshot())
}

func (s *Server) client(w http.ResponseWriter, r *http.Request) {
	id, err := url.PathUnescape(r.PathValue("id"))
	if err != nil || len(id) > 128 || !strings.HasPrefix(id, "mac:") {
		http.Error(w, "invalid client id", http.StatusBadRequest)
		return
	}
	value, ok := s.engine.Client(id)
	if !ok {
		http.Error(w, "client not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) providers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"providers": s.engine.Providers()})
}

func (s *Server) diagnostics(w http.ResponseWriter, _ *http.Request) {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	writeJSON(w, http.StatusOK, map[string]any{
		"uptime_seconds": int64(time.Since(s.startedAt).Seconds()),
		"listener":       map[string]any{"address": s.config.ListenAddress, "port": s.config.Port},
		"configuration": map[string]any{
			"provider":             s.config.Provider,
			"reconcile_interval":   s.config.ReconcileInterval.String(),
			"discovery_interval":   s.config.DiscoveryInterval.String(),
			"lan_interface":        s.config.LANInterface,
			"max_clients":          s.config.MaxClients,
			"max_http_connections": s.config.MaxHTTPConnections,
			"provider_queue_size":  s.config.ProviderQueueSize,
			"max_stream_clients":   s.config.MaxStreamClients,
			"stream_queue_size":    s.config.StreamQueueSize,
			"authentication":       "bearer",
		},
		"providers": s.engine.Providers(), "state": s.engine.Stats(),
		"authentication_failures": s.auth.Failures(),
		"memory":                  map[string]any{"alloc_bytes": memory.Alloc, "sys_bytes": memory.Sys},
		"clock":                   map[string]any{"synchronization": "unverified"},
		"privilege":               map[string]any{"mode": "service process identity", "verified_unprivileged": false},
	})
}

type limitedListener struct {
	net.Listener
	slots     chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func newLimitedListener(inner net.Listener, limit int) *limitedListener {
	return &limitedListener{
		Listener: inner, slots: make(chan struct{}, limit), done: make(chan struct{}),
	}
}

func (l *limitedListener) Accept() (net.Conn, error) {
	select {
	case l.slots <- struct{}{}:
	case <-l.done:
		return nil, net.ErrClosed
	}
	conn, err := l.Listener.Accept()
	if err != nil {
		<-l.slots
		return nil, err
	}
	return &limitedConn{Conn: conn, release: func() { <-l.slots }}, nil
}

func (l *limitedListener) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	return l.Listener.Close()
}

type limitedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	snapshot, events, cancel, err := s.engine.Subscribe()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer cancel()
	conn, rw, err := websocketUpgrade(w, r)
	if err != nil {
		http.Error(w, "WebSocket upgrade required", http.StatusBadRequest)
		return
	}
	defer conn.Close()
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := writeJSONFrame(rw, helloEvent(snapshot)); err != nil {
		return
	}
	initial := protocol.Event{
		Type: "state.snapshot", EventID: snapshot.StreamEpoch + ":snapshot",
		StreamEpoch: snapshot.StreamEpoch, Sequence: snapshot.Sequence,
		Timestamp: time.Now().UTC(), Reason: "consumer_connected", Data: snapshot,
	}
	if err := writeJSONFrame(rw, initial); err != nil {
		return
	}
	if err := rw.Flush(); err != nil {
		return
	}
	done := make(chan struct{})
	controls := make(chan clientControl, 4)
	go monitorClientFramesWithControl(conn, 1024, done, controls)
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()
	for {
		var event protocol.Event
		select {
		case <-s.shutdown:
			event = protocol.Event{
				Type: "stream.shutdown", EventID: snapshot.StreamEpoch + ":shutdown",
				StreamEpoch: snapshot.StreamEpoch, Sequence: s.engine.Snapshot().Sequence,
				Timestamp: time.Now().UTC(), Reason: "service_stopping",
			}
			_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
			_ = writeJSONFrame(rw, event)
			_ = rw.Flush()
			return
		case <-done:
			return
		case <-r.Context().Done():
			return
		case control := <-controls:
			_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
			if err := writeFrame(rw, control.opcode, control.payload); err != nil {
				return
			}
			if err := rw.Flush(); err != nil {
				return
			}
			continue
		case next, ok := <-events:
			if !ok {
				_ = writeFrame(rw, 0x8, []byte{0x03, 0xf3})
				_ = rw.Flush()
				return
			}
			event = next
		case <-heartbeat.C:
			event = protocol.Event{
				Type: "stream.heartbeat", EventID: snapshot.StreamEpoch + ":heartbeat",
				StreamEpoch: snapshot.StreamEpoch, Sequence: s.engine.Snapshot().Sequence,
				Timestamp: time.Now().UTC(),
			}
		}
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := writeJSONFrame(rw, event); err != nil {
			return
		}
		if err := rw.Flush(); err != nil {
			return
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		_ = fmt.Errorf("encode response: %w", err)
	}
}
