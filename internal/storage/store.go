package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/fbongiovanni29/ipfs-oci-registry/internal/types"
	bolt "go.etcd.io/bbolt"
)

var (
	// ErrNotFound is returned when a resource is not found.
	ErrNotFound = errors.New("not found")

	// ErrDatabaseLocked is returned when the database is locked by another process.
	ErrDatabaseLocked = errors.New("database is locked by another process")

	// Bucket names
	bucketDigests      = []byte("digests")
	bucketTags         = []byte("tags")
	bucketRepositories = []byte("repositories")
	bucketUploads      = []byte("uploads")
)

// Store manages the local database for digest→CID mappings and metadata.
type Store struct {
	db   *bolt.DB
	path string
}

// NewStore creates a new storage instance.
func NewStore(path string) (*Store, error) {
	// Check if database file exists and might be locked
	if _, err := os.Stat(path); err == nil {
		// File exists, check if it's locked by trying a non-blocking open
		if locked, pid := isFileLocked(path); locked {
			return nil, fmt.Errorf("%w: file %s is held by PID %d - kill that process or remove the database file",
				ErrDatabaseLocked, path, pid)
		}
	}

	db, err := bolt.Open(path, 0600, &bolt.Options{
		Timeout: 2 * time.Second,
	})
	if err != nil {
		// Provide helpful error message for timeout
		if errors.Is(err, bolt.ErrTimeout) {
			return nil, fmt.Errorf("%w: timeout acquiring lock on %s - another process may be using it, or a previous process crashed. Try: rm %s",
				ErrDatabaseLocked, path, path)
		}
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Create buckets
	err = db.Update(func(tx *bolt.Tx) error {
		for _, bucket := range [][]byte{bucketDigests, bucketTags, bucketRepositories, bucketUploads} {
			if _, err := tx.CreateBucketIfNotExists(bucket); err != nil {
				return fmt.Errorf("failed to create bucket %s: %w", bucket, err)
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db: db, path: path}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Path returns the database file path.
func (s *Store) Path() string {
	return s.path
}

// isFileLocked checks if a file is locked by another process.
// Returns true and the PID if locked, false and 0 otherwise.
func isFileLocked(path string) (bool, int) {
	file, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return false, 0
	}
	defer file.Close()

	// Try to acquire an exclusive lock (non-blocking)
	lock := syscall.Flock_t{
		Type:   syscall.F_WRLCK,
		Whence: 0,
		Start:  0,
		Len:    0,
	}

	// F_GETLK checks if a lock would block and returns info about the blocking lock
	err = syscall.FcntlFlock(file.Fd(), syscall.F_GETLK, &lock)
	if err != nil {
		return false, 0
	}

	// If lock.Type is not F_UNLCK, someone else has the lock
	if lock.Type != syscall.F_UNLCK {
		return true, int(lock.Pid)
	}

	return false, 0
}

// GetMapping retrieves a digest→CID mapping.
func (s *Store) GetMapping(digest string) (*types.BlobMapping, error) {
	var mapping types.BlobMapping

	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketDigests)
		data := bucket.Get([]byte(digest))
		if data == nil {
			return ErrNotFound
		}
		return json.Unmarshal(data, &mapping)
	})

	if err != nil {
		return nil, err
	}

	return &mapping, nil
}

// PutMapping stores a digest→CID mapping.
func (s *Store) PutMapping(mapping *types.BlobMapping) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketDigests)

		data, err := json.Marshal(mapping)
		if err != nil {
			return fmt.Errorf("failed to marshal mapping: %w", err)
		}

		return bucket.Put([]byte(mapping.Digest), data)
	})
}

// DeleteMapping removes a digest→CID mapping.
func (s *Store) DeleteMapping(digest string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketDigests)
		return bucket.Delete([]byte(digest))
	})
}

// HasBlob checks if a blob exists in the store.
func (s *Store) HasBlob(digest string) bool {
	exists := false
	s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketDigests)
		exists = bucket.Get([]byte(digest)) != nil
		return nil
	})
	return exists
}

// GetTag retrieves a tag reference.
func (s *Store) GetTag(repository, tag string) (*types.TagReference, error) {
	var ref types.TagReference

	key := repository + ":" + tag

	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketTags)
		data := bucket.Get([]byte(key))
		if data == nil {
			return ErrNotFound
		}
		return json.Unmarshal(data, &ref)
	})

	if err != nil {
		return nil, err
	}

	return &ref, nil
}

// PutTag stores a tag reference.
func (s *Store) PutTag(ref *types.TagReference) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketTags)

		key := ref.Repository + ":" + ref.Tag

		data, err := json.Marshal(ref)
		if err != nil {
			return fmt.Errorf("failed to marshal tag reference: %w", err)
		}

		return bucket.Put([]byte(key), data)
	})
}

// ListTags returns all tags for a repository.
func (s *Store) ListTags(repository string) ([]string, error) {
	var tags []string
	prefix := []byte(repository + ":")

	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketTags)
		cursor := bucket.Cursor()

		for k, _ := cursor.Seek(prefix); k != nil && hasPrefix(k, prefix); k, _ = cursor.Next() {
			// Extract tag from key "repository:tag"
			tag := string(k[len(prefix):])
			tags = append(tags, tag)
		}

		return nil
	})

	return tags, err
}

// GetRepository retrieves repository metadata.
func (s *Store) GetRepository(name string) (*types.Repository, error) {
	var repo types.Repository

	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketRepositories)
		data := bucket.Get([]byte(name))
		if data == nil {
			return ErrNotFound
		}
		return json.Unmarshal(data, &repo)
	})

	if err != nil {
		return nil, err
	}

	return &repo, nil
}

// PutRepository stores repository metadata.
func (s *Store) PutRepository(repo *types.Repository) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketRepositories)

		data, err := json.Marshal(repo)
		if err != nil {
			return fmt.Errorf("failed to marshal repository: %w", err)
		}

		return bucket.Put([]byte(repo.Name), data)
	})
}

// AddRepositoryManifest adds a manifest digest to a repository.
func (s *Store) AddRepositoryManifest(repoName, digest string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketRepositories)

		var repo types.Repository
		data := bucket.Get([]byte(repoName))
		if data != nil {
			if err := json.Unmarshal(data, &repo); err != nil {
				return err
			}
		} else {
			repo = types.Repository{Name: repoName}
		}

		// Check if manifest already exists
		for _, m := range repo.Manifests {
			if m == digest {
				return nil
			}
		}

		repo.Manifests = append(repo.Manifests, digest)

		data, err := json.Marshal(repo)
		if err != nil {
			return err
		}

		return bucket.Put([]byte(repoName), data)
	})
}

// ListRepositories returns all repository names.
func (s *Store) ListRepositories() ([]string, error) {
	var repos []string

	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketRepositories)
		return bucket.ForEach(func(k, _ []byte) error {
			repos = append(repos, string(k))
			return nil
		})
	})

	return repos, err
}

// GetUpload retrieves an upload session.
func (s *Store) GetUpload(id string) (*types.UploadSession, error) {
	var session types.UploadSession

	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketUploads)
		data := bucket.Get([]byte(id))
		if data == nil {
			return ErrNotFound
		}
		return json.Unmarshal(data, &session)
	})

	if err != nil {
		return nil, err
	}

	return &session, nil
}

// PutUpload stores an upload session.
func (s *Store) PutUpload(session *types.UploadSession) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketUploads)

		data, err := json.Marshal(session)
		if err != nil {
			return fmt.Errorf("failed to marshal upload session: %w", err)
		}

		return bucket.Put([]byte(session.ID), data)
	})
}

// DeleteUpload removes an upload session.
func (s *Store) DeleteUpload(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketUploads)
		return bucket.Delete([]byte(id))
	})
}

// hasPrefix checks if a byte slice has a given prefix.
func hasPrefix(s, prefix []byte) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i, b := range prefix {
		if s[i] != b {
			return false
		}
	}
	return true
}

// Stats returns statistics about the store.
type Stats struct {
	BlobCount       int
	TagCount        int
	RepositoryCount int
	UploadCount     int
}

// ForEachMapping iterates over all digest mappings.
// The callback returns true to continue, false to stop.
func (s *Store) ForEachMapping(fn func(mapping *types.BlobMapping) bool) error {
	return s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketDigests)
		return bucket.ForEach(func(k, v []byte) error {
			var mapping types.BlobMapping
			if err := json.Unmarshal(v, &mapping); err != nil {
				return nil // skip malformed entries
			}
			if !fn(&mapping) {
				return fmt.Errorf("stop") // stop iteration
			}
			return nil
		})
	})
}

// ListStaleUploads returns upload sessions older than maxAge.
func (s *Store) ListStaleUploads(maxAge time.Duration) ([]*types.UploadSession, error) {
	var stale []*types.UploadSession
	cutoff := time.Now().Add(-maxAge)

	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketUploads)
		return bucket.ForEach(func(k, v []byte) error {
			var session types.UploadSession
			if err := json.Unmarshal(v, &session); err != nil {
				return nil
			}
			if session.StartedAt.Before(cutoff) {
				stale = append(stale, &session)
			}
			return nil
		})
	})

	return stale, err
}

// RemoveTempFile removes a temporary upload file, ignoring errors.
func RemoveTempFile(path string) {
	if path != "" {
		os.Remove(path)
	}
}

// GetStats returns statistics about the store.
func (s *Store) GetStats() (*Stats, error) {
	var stats Stats

	err := s.db.View(func(tx *bolt.Tx) error {
		stats.BlobCount = tx.Bucket(bucketDigests).Stats().KeyN
		stats.TagCount = tx.Bucket(bucketTags).Stats().KeyN
		stats.RepositoryCount = tx.Bucket(bucketRepositories).Stats().KeyN
		stats.UploadCount = tx.Bucket(bucketUploads).Stats().KeyN
		return nil
	})

	return &stats, err
}
