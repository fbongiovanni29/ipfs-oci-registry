package metrics

import (
	"os"

	"github.com/fbongiovanni29/ipfs-oci-registry/internal/storage"
	"github.com/prometheus/client_golang/prometheus"
)

// StorageCollector implements prometheus.Collector and reports storage stats on each scrape.
type StorageCollector struct {
	store *storage.Store

	blobsDesc  *prometheus.Desc
	tagsDesc   *prometheus.Desc
	reposDesc  *prometheus.Desc
	dbSizeDesc *prometheus.Desc
}

// RegisterStorageCollector creates and registers a storage collector.
func RegisterStorageCollector(store *storage.Store) {
	prometheus.MustRegister(&StorageCollector{
		store: store,
		blobsDesc: prometheus.NewDesc(
			"registry_storage_blobs",
			"Number of blob mappings in the store.",
			nil, nil,
		),
		tagsDesc: prometheus.NewDesc(
			"registry_storage_tags",
			"Number of tag references in the store.",
			nil, nil,
		),
		reposDesc: prometheus.NewDesc(
			"registry_storage_repositories",
			"Number of repositories in the store.",
			nil, nil,
		),
		dbSizeDesc: prometheus.NewDesc(
			"registry_storage_db_size_bytes",
			"BoltDB database file size in bytes.",
			nil, nil,
		),
	})
}

func (c *StorageCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.blobsDesc
	ch <- c.tagsDesc
	ch <- c.reposDesc
	ch <- c.dbSizeDesc
}

func (c *StorageCollector) Collect(ch chan<- prometheus.Metric) {
	stats, err := c.store.GetStats()
	if err == nil {
		ch <- prometheus.MustNewConstMetric(c.blobsDesc, prometheus.GaugeValue, float64(stats.BlobCount))
		ch <- prometheus.MustNewConstMetric(c.tagsDesc, prometheus.GaugeValue, float64(stats.TagCount))
		ch <- prometheus.MustNewConstMetric(c.reposDesc, prometheus.GaugeValue, float64(stats.RepositoryCount))
	}

	if info, err := os.Stat(c.store.Path()); err == nil {
		ch <- prometheus.MustNewConstMetric(c.dbSizeDesc, prometheus.GaugeValue, float64(info.Size()))
	}
}
