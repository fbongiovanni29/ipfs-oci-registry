package gc

import (
	"context"
	"time"

	"github.com/fbongiovanni29/ipfs-oci-registry/internal/config"
	"github.com/fbongiovanni29/ipfs-oci-registry/internal/ipfs"
	"github.com/fbongiovanni29/ipfs-oci-registry/internal/storage"
	"github.com/fbongiovanni29/ipfs-oci-registry/internal/types"
	"github.com/rs/zerolog"
)

// Collector performs periodic garbage collection of old content.
type Collector struct {
	store      *storage.Store
	ipfsClient *ipfs.Client
	config     config.GCConfig
	logger     zerolog.Logger
	cancel     context.CancelFunc
}

// NewCollector creates a new garbage collector.
func NewCollector(store *storage.Store, ipfsClient *ipfs.Client, cfg config.GCConfig, logger zerolog.Logger) *Collector {
	return &Collector{
		store:      store,
		ipfsClient: ipfsClient,
		config:     cfg,
		logger:     logger,
	}
}

// Start begins the GC loop in a background goroutine.
func (c *Collector) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel

	go c.run(ctx)
	c.logger.Info().
		Dur("interval", c.config.Interval).
		Dur("max_age", c.config.MaxAge).
		Bool("dry_run", c.config.DryRun).
		Msg("garbage collector started")
}

// Stop halts the GC loop.
func (c *Collector) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
}

func (c *Collector) run(ctx context.Context) {
	// Run once immediately on startup
	c.collect(ctx)

	ticker := time.NewTicker(c.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.collect(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (c *Collector) collect(ctx context.Context) {
	cutoff := time.Now().Add(-c.config.MaxAge)

	c.logger.Info().Time("cutoff", cutoff).Msg("starting GC sweep")

	var deleted, unpinned, errors int

	err := c.store.ForEachMapping(func(mapping *types.BlobMapping) bool {
		if ctx.Err() != nil {
			return false // stop iteration
		}

		if mapping.CreatedAt.After(cutoff) {
			return true // skip, not old enough
		}

		c.logger.Debug().
			Str("digest", mapping.Digest).
			Str("cid", mapping.CID).
			Str("source", mapping.Source).
			Time("created", mapping.CreatedAt).
			Bool("dry_run", c.config.DryRun).
			Msg("GC candidate")

		if c.config.DryRun {
			deleted++
			return true
		}

		// Unpin from IPFS first
		if mapping.CID != "" {
			if err := c.ipfsClient.Unpin(ctx, mapping.CID); err != nil {
				c.logger.Warn().Err(err).Str("cid", mapping.CID).Msg("failed to unpin CID")
				errors++
			} else {
				unpinned++
			}
		}

		// Delete from store
		if err := c.store.DeleteMapping(mapping.Digest); err != nil {
			c.logger.Warn().Err(err).Str("digest", mapping.Digest).Msg("failed to delete mapping")
			errors++
		} else {
			deleted++
		}

		return true
	})

	if err != nil {
		c.logger.Error().Err(err).Msg("GC sweep failed")
		return
	}

	// Also clean up stale upload sessions (older than 24 hours)
	staleUploads, err := c.store.ListStaleUploads(24 * time.Hour)
	if err == nil {
		for _, session := range staleUploads {
			if !c.config.DryRun {
				c.store.DeleteUpload(session.ID)
				// Temp file may already be gone, ignore errors
				storage.RemoveTempFile(session.TempPath)
			}
			c.logger.Debug().Str("upload_id", session.ID).Msg("cleaned up stale upload")
		}
	}

	c.logger.Info().
		Int("deleted", deleted).
		Int("unpinned", unpinned).
		Int("errors", errors).
		Int("stale_uploads", len(staleUploads)).
		Bool("dry_run", c.config.DryRun).
		Msg("GC sweep complete")
}
