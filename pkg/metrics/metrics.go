package metrics

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

var (
	ErrAlreadyStarted = errors.New("metrics exporter already started")

	tunnelMetricsInst *TunnelMetrics
	tunnelMetricsOnce sync.Once
)

// Exporter exports Prometheus metrics
type Exporter struct {
	addr   string
	server *http.Server
	logger *zap.Logger
	mu     sync.Mutex
}

// NewExporter creates a new metrics exporter
func NewExporter(addr string, logger *zap.Logger) *Exporter {
	return &Exporter{
		addr:   addr,
		logger: logger,
	}
}

// Start binds the metrics endpoint synchronously and then serves it in the
// background. Binding errors (e.g. port already in use) are returned to the
// caller instead of surfacing as an async log line after a "successful"
// startup. A second Start returns ErrAlreadyStarted instead of silently
// leaking the first listener.
func (e *Exporter) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.server != nil {
		return ErrAlreadyStarted
	}

	ln, err := net.Listen("tcp", e.addr)
	if err != nil {
		return fmt.Errorf("metrics listener on %s: %w", e.addr, err)
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	e.server = &http.Server{
		Addr:    e.addr,
		Handler: mux,
	}

	e.logger.Info("starting metrics server", zap.String("addr", e.addr))

	go func() {
		if err := e.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			e.logger.Error("metrics server error", zap.Error(err))
		}
	}()

	return nil
}

// Stop stops the metrics server
func (e *Exporter) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.server == nil {
		return nil
	}
	srv := e.server
	e.server = nil
	return srv.Close()
}

// TunnelMetrics holds tunnel-related metrics
type TunnelMetrics struct {
	sessionsGauge     prometheus.Gauge
	streamsGauge      prometheus.Gauge
	handshakeDuration prometheus.Histogram
	streamEvents      *prometheus.CounterVec
}

func newTunnelMetrics() *TunnelMetrics {
	return &TunnelMetrics{
		sessionsGauge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "tunnel_sessions_active",
			Help: "Number of active tunnel sessions",
		}),
		streamsGauge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "tunnel_streams_active",
			Help: "Number of active yamux streams",
		}),
		handshakeDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "tunnel_handshake_duration_seconds",
			Help:    "Duration of handshake process",
			Buckets: prometheus.DefBuckets,
		}),
		streamEvents: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "tunnel_stream_events_total",
				Help: "Total number of stream events",
			},
			[]string{"type"},
		),
	}
}

// TunnelCollectors returns prometheus collectors
func TunnelCollectors() []prometheus.Collector {
	m := getTunnelMetrics()
	return []prometheus.Collector{
		m.sessionsGauge,
		m.streamsGauge,
		m.handshakeDuration,
		m.streamEvents,
	}
}

func getTunnelMetrics() *TunnelMetrics {
	tunnelMetricsOnce.Do(func() {
		tunnelMetricsInst = newTunnelMetrics()
	})
	return tunnelMetricsInst
}

func (m *TunnelMetrics) SetSessions(count int) {
	m.sessionsGauge.Set(float64(count))
}

func (m *TunnelMetrics) AddStreams(delta int) {
	m.streamsGauge.Add(float64(delta))
}

func (m *TunnelMetrics) SetAccepting(accepting bool) {
	// No-op for now
}

func (m *TunnelMetrics) ObserveHandshake(duration time.Duration) {
	m.handshakeDuration.Observe(duration.Seconds())
}

func (m *TunnelMetrics) IncStreamEvent(eventType string) {
	m.streamEvents.WithLabelValues(eventType).Inc()
}

func (m *TunnelMetrics) IncIncoming() {
	m.streamEvents.WithLabelValues("incoming").Inc()
}

func (m *TunnelMetrics) IncKeepalive() {
	m.streamEvents.WithLabelValues("keepalive").Inc()
}

func (m *TunnelMetrics) IncBackoff() {
	m.streamEvents.WithLabelValues("backoff").Inc()
}
