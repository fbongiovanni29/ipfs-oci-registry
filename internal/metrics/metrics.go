package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	// HTTP request metrics
	HTTPRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "registry_http_requests_total",
			Help: "Total HTTP requests by method, route, and status code.",
		},
		[]string{"method", "route", "status_code"},
	)

	HTTPRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "registry_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "route"},
	)

	HTTPResponseSize = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "registry_http_response_size_bytes",
			Help:    "HTTP response size in bytes.",
			Buckets: []float64{100, 1000, 10000, 100000, 1e6, 10e6, 100e6, 1e9},
		},
		[]string{"method", "route"},
	)

	// Resolution metrics (cache hit/miss)
	ResolveTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "registry_resolve_total",
			Help: "Content resolution attempts by type, source, and result.",
		},
		[]string{"type", "source", "result"},
	)

	// IPFS operation metrics
	IPFSOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "registry_ipfs_operations_total",
			Help: "IPFS operations by operation type and result.",
		},
		[]string{"operation", "result"},
	)

	IPFSOperationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "registry_ipfs_operation_duration_seconds",
			Help:    "IPFS operation duration in seconds.",
			Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60},
		},
		[]string{"operation"},
	)

	// Federation metrics
	FederationMessagesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "registry_federation_messages_total",
			Help: "Federation messages by direction and type.",
		},
		[]string{"direction", "type"},
	)

	FederationQueryDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "registry_federation_query_duration_seconds",
			Help:    "Federation query duration in seconds.",
			Buckets: []float64{.01, .05, .1, .25, .5, 1, 2},
		},
	)

	// Upstream metrics
	UpstreamRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "registry_upstream_requests_total",
			Help: "Upstream registry requests by registry, type, and result.",
		},
		[]string{"registry", "type", "result"},
	)

	UpstreamRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "registry_upstream_request_duration_seconds",
			Help:    "Upstream registry request duration in seconds.",
			Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30},
		},
		[]string{"registry", "type"},
	)

	// GC metrics
	GCRunsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "registry_gc_runs_total",
			Help: "Total garbage collection runs.",
		},
	)

	GCDeletedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "registry_gc_deleted_total",
			Help: "Total items deleted by garbage collection.",
		},
	)

	GCErrorsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "registry_gc_errors_total",
			Help: "Total errors during garbage collection.",
		},
	)

	GCDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "registry_gc_duration_seconds",
			Help:    "Garbage collection sweep duration in seconds.",
			Buckets: []float64{.1, .5, 1, 5, 10, 30, 60, 300},
		},
	)

	// Rate limit metrics
	RateLimitBlockedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "registry_ratelimit_blocked_total",
			Help: "Total requests blocked by rate limiting.",
		},
	)
)

func init() {
	prometheus.MustRegister(
		// HTTP
		HTTPRequestsTotal,
		HTTPRequestDuration,
		HTTPResponseSize,
		// Resolution
		ResolveTotal,
		// IPFS
		IPFSOperationsTotal,
		IPFSOperationDuration,
		// Federation
		FederationMessagesTotal,
		FederationQueryDuration,
		// Upstream
		UpstreamRequestsTotal,
		UpstreamRequestDuration,
		// GC
		GCRunsTotal,
		GCDeletedTotal,
		GCErrorsTotal,
		GCDuration,
		// Rate limit
		RateLimitBlockedTotal,
	)
}
