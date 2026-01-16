package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/containerish/ipfs-oci-registry/internal/config"
	"github.com/containerish/ipfs-oci-registry/internal/federation"
	"github.com/containerish/ipfs-oci-registry/internal/ipfs"
	"github.com/containerish/ipfs-oci-registry/internal/registry"
	"github.com/containerish/ipfs-oci-registry/internal/storage"
	"github.com/containerish/ipfs-oci-registry/internal/upstream"
	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	var (
		configPath  string
		showVersion bool
	)

	flag.StringVar(&configPath, "config", "", "Path to configuration file")
	flag.BoolVar(&showVersion, "version", false, "Show version information")
	flag.Parse()

	if showVersion {
		fmt.Printf("IPFS OCI Registry v%s\n", version)
		fmt.Printf("  Commit:     %s\n", commit)
		fmt.Printf("  Build Date: %s\n", buildDate)
		os.Exit(0)
	}

	// Setup logger
	logger := setupLogger()

	// Load configuration
	cfg, err := loadConfig(configPath)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to load configuration")
	}

	// Apply log level from config
	logger = applyLogLevel(logger, cfg.Logging.Level)

	logger.Info().
		Str("version", version).
		Str("address", cfg.Server.Address).
		Msg("starting IPFS OCI registry")

	// Ensure storage directories exist
	if err := ensureDirectories(cfg); err != nil {
		logger.Fatal().Err(err).Msg("failed to create directories")
	}

	// Initialize IPFS client
	ipfsClient := ipfs.NewClient(cfg.IPFS.APIURL, cfg.IPFS.Timeout, cfg.IPFS.PinContent)

	// Check IPFS connectivity
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if !ipfsClient.IsAvailable(ctx) {
		cancel()
		logger.Warn().
			Str("api_url", cfg.IPFS.APIURL).
			Msg("IPFS node not reachable - some features will be unavailable")
	} else {
		id, _ := ipfsClient.ID(ctx)
		cancel()
		logger.Info().
			Str("peer_id", id.ID).
			Str("agent", id.AgentVersion).
			Msg("connected to IPFS node")
	}

	// Initialize storage
	store, err := storage.NewStore(cfg.Storage.Database)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialize storage")
	}
	defer store.Close()

	// Initialize upstream client
	upstreamClient := upstream.NewClient(cfg.Upstreams, logger)

	// Initialize federation (if enabled)
	var fed registry.Federation
	if cfg.Federation.Enabled {
		f, err := federation.NewFederation(ipfsClient, &cfg.Federation, logger)
		if err != nil {
			logger.Warn().Err(err).Msg("failed to initialize federation")
			fed = &federation.NullFederation{}
		} else {
			if err := f.Start(); err != nil {
				logger.Warn().Err(err).Msg("failed to start federation")
				fed = &federation.NullFederation{}
			} else {
				fed = f
				defer f.Stop()
			}
		}
	} else {
		fed = &federation.NullFederation{}
	}

	// Initialize registry handler
	handler, err := registry.NewHandler(store, ipfsClient, upstreamClient, fed, cfg, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to create registry handler")
	}

	// Setup HTTP router
	router := mux.NewRouter()
	router.Use(loggingMiddleware(logger))
	router.Use(corsMiddleware())
	handler.RegisterRoutes(router)

	// Create HTTP server
	server := &http.Server{
		Addr:         cfg.Server.Address,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Minute, // Long timeout for large blob transfers
		IdleTimeout:  120 * time.Second,
	}

	// Start server
	go func() {
		var err error
		if cfg.Server.TLS.Enabled {
			logger.Info().Msg("starting HTTPS server")
			err = server.ListenAndServeTLS(cfg.Server.TLS.Cert, cfg.Server.TLS.Key)
		} else {
			logger.Info().Msg("starting HTTP server")
			err = server.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			logger.Fatal().Err(err).Msg("server error")
		}
	}()

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	logger.Info().Msg("shutting down server...")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("shutdown error")
	}

	logger.Info().Msg("server stopped")
}

func loadConfig(path string) (*config.Config, error) {
	if path != "" {
		return config.Load(path)
	}

	// Try default locations
	defaultPaths := []string{
		"config.yaml",
		"/etc/oci-ipfs/config.yaml",
		filepath.Join(os.Getenv("HOME"), ".config/oci-ipfs/config.yaml"),
	}

	for _, p := range defaultPaths {
		if _, err := os.Stat(p); err == nil {
			return config.Load(p)
		}
	}

	// Use defaults
	return config.DefaultConfig(), nil
}

func ensureDirectories(cfg *config.Config) error {
	dirs := []string{
		filepath.Dir(cfg.Storage.Database),
		cfg.Storage.TempDir,
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return nil
}

func setupLogger() zerolog.Logger {
	output := zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: time.RFC3339,
	}
	return zerolog.New(output).With().Timestamp().Logger()
}

func applyLogLevel(logger zerolog.Logger, level string) zerolog.Logger {
	switch level {
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "info":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
	return logger
}

func loggingMiddleware(logger zerolog.Logger) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap response writer to capture status
			ww := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(ww, r)

			logger.Debug().
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Int("status", ww.status).
				Dur("duration", time.Since(start)).
				Str("remote", r.RemoteAddr).
				Msg("request")
		})
	}
}

func corsMiddleware() mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Docker-Content-Digest")
			w.Header().Set("Access-Control-Expose-Headers", "Docker-Content-Digest, Location, Docker-Upload-UUID")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
