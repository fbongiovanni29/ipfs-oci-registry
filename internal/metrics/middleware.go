package metrics

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"
)

// HTTPMiddleware returns a gorilla/mux middleware that records HTTP metrics.
func HTTPMiddleware() mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap response writer to capture status and size
			rw := &metricsResponseWriter{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rw, r)

			duration := time.Since(start).Seconds()

			// Use mux route template for low-cardinality labels
			route := "unmatched"
			if cr := mux.CurrentRoute(r); cr != nil {
				if tmpl, err := cr.GetPathTemplate(); err == nil {
					route = tmpl
				}
			}

			method := r.Method
			status := fmt.Sprintf("%d", rw.status)

			HTTPRequestsTotal.WithLabelValues(method, route, status).Inc()
			HTTPRequestDuration.WithLabelValues(method, route).Observe(duration)
			HTTPResponseSize.WithLabelValues(method, route).Observe(float64(rw.bytesWritten))
		})
	}
}

type metricsResponseWriter struct {
	http.ResponseWriter
	status       int
	bytesWritten int64
}

func (w *metricsResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *metricsResponseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytesWritten += int64(n)
	return n, err
}
