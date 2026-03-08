package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestHTTPMiddleware(t *testing.T) {
	router := mux.NewRouter()
	router.Use(HTTPMiddleware())
	router.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}).Methods(http.MethodGet)

	req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	// Initialize Vec metrics so they appear in output (Vec types only
	// emit HELP/TYPE after at least one label combination is observed).
	ResolveTotal.WithLabelValues("test", "test", "test").Inc()
	IPFSOperationsTotal.WithLabelValues("test", "test").Inc()
	FederationMessagesTotal.WithLabelValues("test", "test").Inc()
	UpstreamRequestsTotal.WithLabelValues("test", "test", "test").Inc()

	router := mux.NewRouter()
	router.Handle("/metrics", promhttp.Handler()).Methods(http.MethodGet)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()

	expectedHelp := []string{
		"HELP registry_http_requests_total",
		"HELP registry_resolve_total",
		"HELP registry_ipfs_operations_total",
		"HELP registry_federation_messages_total",
		"HELP registry_upstream_requests_total",
		"HELP registry_gc_runs_total",
		"HELP registry_ratelimit_blocked_total",
	}

	for _, h := range expectedHelp {
		if !strings.Contains(body, h) {
			t.Errorf("expected %s in /metrics output", h)
		}
	}
}

func TestMetricsMiddlewareRouteLabel(t *testing.T) {
	router := mux.NewRouter()
	router.Use(HTTPMiddleware())
	router.HandleFunc("/v2/{name:.*}/manifests/{reference}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}).Methods(http.MethodGet)

	// Hit with a specific path — route label should be the template, not the actual path
	req := httptest.NewRequest(http.MethodGet, "/v2/docker.io/library/nginx/manifests/latest", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	// Verify the metric was recorded (we can't easily check labels, but we can verify no panic)
}
