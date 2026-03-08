package federation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sync"
	"time"

	"github.com/fbongiovanni29/ipfs-oci-registry/internal/config"
	"github.com/fbongiovanni29/ipfs-oci-registry/internal/ipfs"
	"github.com/fbongiovanni29/ipfs-oci-registry/internal/metrics"
	"github.com/fbongiovanni29/ipfs-oci-registry/internal/types"
	"github.com/rs/zerolog"
)

// TagHandler is called when a tag update is received from federation.
type TagHandler func(repo, tag, digest string)

// MappingHandler is called when a mapping is received from federation.
type MappingHandler func(mapping *types.BlobMapping)

// Federation handles peer-to-peer communication for digest→CID mapping discovery.
type Federation struct {
	ipfs           *ipfs.Client
	config         *config.FederationConfig
	logger         zerolog.Logger
	peerID         string
	cache          *mappingCache
	tagCache       *tagCache
	queries        *queryManager
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	tagHandler     TagHandler
	mappingHandler MappingHandler
}

// mappingCache stores recently received mappings from peers.
type mappingCache struct {
	mu       sync.RWMutex
	mappings map[string]*types.BlobMapping
	ttl      time.Duration
}

func newMappingCache(ttl time.Duration) *mappingCache {
	return &mappingCache{
		mappings: make(map[string]*types.BlobMapping),
		ttl:      ttl,
	}
}

func (c *mappingCache) Get(digest string) (*types.BlobMapping, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.mappings[digest]
	return m, ok
}

func (c *mappingCache) Set(mapping *types.BlobMapping) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mappings[mapping.Digest] = mapping

	// Schedule cleanup
	go func() {
		time.Sleep(c.ttl)
		c.mu.Lock()
		defer c.mu.Unlock()
		// Only delete if it's the same mapping (not updated)
		if m, ok := c.mappings[mapping.Digest]; ok && m.CID == mapping.CID {
			delete(c.mappings, mapping.Digest)
		}
	}()
}

// tagCache stores recently received tags from peers.
type tagCache struct {
	mu   sync.RWMutex
	tags map[string]string // "repo:tag" -> digest
	ttl  time.Duration
}

func newTagCache(ttl time.Duration) *tagCache {
	return &tagCache{
		tags: make(map[string]string),
		ttl:  ttl,
	}
}

func (c *tagCache) Key(repo, tag string) string {
	return repo + ":" + tag
}

func (c *tagCache) Get(repo, tag string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	digest, ok := c.tags[c.Key(repo, tag)]
	return digest, ok
}

func (c *tagCache) Set(repo, tag, digest string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := c.Key(repo, tag)
	c.tags[key] = digest

	// Schedule cleanup
	go func() {
		time.Sleep(c.ttl)
		c.mu.Lock()
		defer c.mu.Unlock()
		// Only delete if it's the same digest (not updated)
		if d, ok := c.tags[key]; ok && d == digest {
			delete(c.tags, key)
		}
	}()
}

// queryManager handles pending queries waiting for responses.
type queryManager struct {
	mu      sync.RWMutex
	pending map[string]chan *types.BlobMapping
}

func newQueryManager() *queryManager {
	return &queryManager{
		pending: make(map[string]chan *types.BlobMapping),
	}
}

func (q *queryManager) CreateQuery(requestID string) chan *types.BlobMapping {
	ch := make(chan *types.BlobMapping, 1)
	q.mu.Lock()
	q.pending[requestID] = ch
	q.mu.Unlock()
	return ch
}

func (q *queryManager) Respond(requestID string, mapping *types.BlobMapping) bool {
	q.mu.Lock()
	ch, ok := q.pending[requestID]
	if ok {
		delete(q.pending, requestID)
	}
	q.mu.Unlock()

	if ok {
		select {
		case ch <- mapping:
			return true
		default:
		}
	}
	return false
}

func (q *queryManager) Cancel(requestID string) {
	q.mu.Lock()
	delete(q.pending, requestID)
	q.mu.Unlock()
}

// NewFederation creates a new federation instance.
func NewFederation(ipfsClient *ipfs.Client, cfg *config.FederationConfig, logger zerolog.Logger) (*Federation, error) {
	ctx, cancel := context.WithCancel(context.Background())

	f := &Federation{
		ipfs:     ipfsClient,
		config:   cfg,
		logger:   logger.With().Str("component", "federation").Logger(),
		cache:    newMappingCache(5 * time.Minute),
		tagCache: newTagCache(5 * time.Minute),
		queries:  newQueryManager(),
		ctx:      ctx,
		cancel:   cancel,
	}

	return f, nil
}

// SetTagHandler sets the callback for when tags are received from federation.
func (f *Federation) SetTagHandler(handler TagHandler) {
	f.tagHandler = handler
}

// SetMappingHandler sets the callback for when mappings are received from federation.
func (f *Federation) SetMappingHandler(handler MappingHandler) {
	f.mappingHandler = handler
}

// Start begins listening for federation messages.
func (f *Federation) Start() error {
	// Get our peer ID
	id, err := f.ipfs.ID(f.ctx)
	if err != nil {
		return err
	}
	f.peerID = id.ID
	f.logger.Info().Str("peer_id", f.peerID).Msg("starting federation")

	// Subscribe to the mapping topic
	messages, err := f.ipfs.PubsubSubscribe(f.ctx, f.config.Topic)
	if err != nil {
		return err
	}

	f.wg.Add(1)
	go f.handleMessages(messages)

	return nil
}

// Stop stops the federation layer.
func (f *Federation) Stop() {
	f.cancel()
	f.wg.Wait()
}

// handleMessages processes incoming pubsub messages.
func (f *Federation) handleMessages(messages <-chan ipfs.PubsubMessage) {
	defer f.wg.Done()

	for {
		select {
		case <-f.ctx.Done():
			return
		case msg, ok := <-messages:
			if !ok {
				return
			}
			f.processMessage(msg)
		}
	}
}

// processMessage handles a single pubsub message.
func (f *Federation) processMessage(msg ipfs.PubsubMessage) {
	// Decode base64 data (IPFS pubsub encodes data)
	data, err := base64.StdEncoding.DecodeString(string(msg.Data))
	if err != nil {
		// Try raw data
		data = msg.Data
	}

	var fedMsg types.FederationMessage
	if err := json.Unmarshal(data, &fedMsg); err != nil {
		f.logger.Debug().Err(err).Msg("failed to decode federation message")
		return
	}

	// Ignore our own messages
	if fedMsg.PeerID == f.peerID {
		return
	}

	metrics.FederationMessagesTotal.WithLabelValues("received", fedMsg.Type).Inc()

	switch fedMsg.Type {
	case "mapping":
		f.handleMappingAnnouncement(fedMsg)
	case "query":
		f.handleQuery(fedMsg)
	case "response":
		f.handleResponse(fedMsg)
	case "tag":
		f.handleTagAnnouncement(fedMsg)
	default:
		f.logger.Debug().Str("type", fedMsg.Type).Msg("unknown message type")
	}
}

// handleMappingAnnouncement processes a new mapping announcement.
func (f *Federation) handleMappingAnnouncement(msg types.FederationMessage) {
	mapping := &types.BlobMapping{
		Digest:    msg.Digest,
		CID:       msg.CID,
		Size:      msg.Size,
		MediaType: msg.MediaType,
		Source:    "federation:" + msg.PeerID,
		CreatedAt: msg.Timestamp,
	}

	f.cache.Set(mapping)

	// Notify handler if set
	if f.mappingHandler != nil {
		f.mappingHandler(mapping)
	}

	f.logger.Debug().
		Str("digest", msg.Digest).
		Str("cid", msg.CID).
		Str("from", msg.PeerID).
		Msg("received mapping announcement")
}

// handleTagAnnouncement processes a tag update from another peer.
func (f *Federation) handleTagAnnouncement(msg types.FederationMessage) {
	// Cache the tag locally
	f.tagCache.Set(msg.Repository, msg.Tag, msg.Digest)

	// Notify handler if set (this will update the local store)
	if f.tagHandler != nil {
		f.tagHandler(msg.Repository, msg.Tag, msg.Digest)
	}

	f.logger.Debug().
		Str("repository", msg.Repository).
		Str("tag", msg.Tag).
		Str("digest", msg.Digest).
		Str("from", msg.PeerID).
		Msg("received tag announcement")
}

// handleQuery responds to a digest query from another peer.
func (f *Federation) handleQuery(msg types.FederationMessage) {
	// Check if we have this digest in our local cache
	// Note: In a full implementation, we'd check the local store
	mapping, ok := f.cache.Get(msg.Digest)
	if !ok {
		return
	}

	// Send response
	response := types.FederationMessage{
		Type:      "response",
		Digest:    mapping.Digest,
		CID:       mapping.CID,
		Size:      mapping.Size,
		MediaType: mapping.MediaType,
		PeerID:    f.peerID,
		RequestID: msg.RequestID,
		Timestamp: time.Now(),
	}

	f.publish(response)
}

// handleResponse processes a query response.
func (f *Federation) handleResponse(msg types.FederationMessage) {
	mapping := &types.BlobMapping{
		Digest:    msg.Digest,
		CID:       msg.CID,
		Size:      msg.Size,
		MediaType: msg.MediaType,
		Source:    "federation:" + msg.PeerID,
		CreatedAt: msg.Timestamp,
	}

	// Try to deliver to waiting query
	if f.queries.Respond(msg.RequestID, mapping) {
		f.logger.Debug().
			Str("digest", msg.Digest).
			Str("from", msg.PeerID).
			Msg("received query response")
	}

	// Also cache it
	f.cache.Set(mapping)
}

// QueryDigest queries the federation for a digest→CID mapping.
func (f *Federation) QueryDigest(ctx context.Context, digest string) (*types.BlobMapping, error) {
	start := time.Now()
	defer func() { metrics.FederationQueryDuration.Observe(time.Since(start).Seconds()) }()

	// Check cache first
	if mapping, ok := f.cache.Get(digest); ok {
		return mapping, nil
	}

	// Send query
	requestID := generateRequestID()
	responseCh := f.queries.CreateQuery(requestID)
	defer f.queries.Cancel(requestID)

	query := types.FederationMessage{
		Type:      "query",
		Digest:    digest,
		PeerID:    f.peerID,
		RequestID: requestID,
		Timestamp: time.Now(),
	}

	metrics.FederationMessagesTotal.WithLabelValues("sent", "query").Inc()
	if err := f.publish(query); err != nil {
		return nil, err
	}

	// Wait for response with timeout
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case mapping := <-responseCh:
		return mapping, nil
	}
}

// Announce announces a new mapping to the federation.
func (f *Federation) Announce(mapping *types.BlobMapping) error {
	if !f.config.AnnounceNewContent {
		return nil
	}

	msg := types.FederationMessage{
		Type:      "mapping",
		Digest:    mapping.Digest,
		CID:       mapping.CID,
		Size:      mapping.Size,
		MediaType: mapping.MediaType,
		PeerID:    f.peerID,
		Timestamp: time.Now(),
	}

	metrics.FederationMessagesTotal.WithLabelValues("sent", "mapping").Inc()
	return f.publish(msg)
}

// AnnounceTag announces a tag update to the federation.
func (f *Federation) AnnounceTag(repo, tag, digest string) error {
	if !f.config.AnnounceNewContent {
		return nil
	}

	msg := types.FederationMessage{
		Type:       "tag",
		Repository: repo,
		Tag:        tag,
		Digest:     digest,
		PeerID:     f.peerID,
		Timestamp:  time.Now(),
	}

	f.logger.Debug().
		Str("repository", repo).
		Str("tag", tag).
		Str("digest", digest).
		Msg("announcing tag to federation")

	return f.publish(msg)
}

// GetCachedTag returns a tag from the federation cache if available.
func (f *Federation) GetCachedTag(repo, tag string) (string, bool) {
	return f.tagCache.Get(repo, tag)
}

// publish sends a message to the federation topic.
func (f *Federation) publish(msg types.FederationMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	return f.ipfs.PubsubPublish(f.ctx, f.config.Topic, data)
}

// generateRequestID generates a unique request ID.
func generateRequestID() string {
	b := make([]byte, 16)
	// Use time-based ID for simplicity (could use crypto/rand)
	t := time.Now().UnixNano()
	for i := 0; i < 8; i++ {
		b[i] = byte(t >> (i * 8))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// NullFederation is a no-op federation implementation for when federation is disabled.
type NullFederation struct{}

func (n *NullFederation) QueryDigest(ctx context.Context, digest string) (*types.BlobMapping, error) {
	return nil, nil
}

func (n *NullFederation) Announce(mapping *types.BlobMapping) error {
	return nil
}

func (n *NullFederation) AnnounceTag(repo, tag, digest string) error {
	return nil
}

func (n *NullFederation) GetCachedTag(repo, tag string) (string, bool) {
	return "", false
}

func (n *NullFederation) SetTagHandler(handler TagHandler) {}

func (n *NullFederation) SetMappingHandler(handler MappingHandler) {}
