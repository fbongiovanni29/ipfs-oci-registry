package ipfs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"time"

	"github.com/fbongiovanni29/ipfs-oci-registry/internal/metrics"
)

// Client communicates with a Kubo (go-ipfs) node via the HTTP API.
type Client struct {
	apiURL     string
	httpClient *http.Client
	pinContent bool
}

// AddResponse is the response from the IPFS add endpoint.
type AddResponse struct {
	Name string `json:"Name"`
	Hash string `json:"Hash"`
	Size string `json:"Size"`
}

// IDResponse is the response from the IPFS id endpoint.
type IDResponse struct {
	ID              string   `json:"ID"`
	PublicKey       string   `json:"PublicKey"`
	Addresses       []string `json:"Addresses"`
	AgentVersion    string   `json:"AgentVersion"`
	ProtocolVersion string   `json:"ProtocolVersion"`
}

// PubsubMessage is a message received from pubsub subscription.
type PubsubMessage struct {
	From     string `json:"from"`
	Data     []byte `json:"data"`
	Seqno    string `json:"seqno"`
	TopicIDs []string `json:"topicIDs"`
}

// NewClient creates a new IPFS client.
func NewClient(apiURL string, timeout time.Duration, pinContent bool) *Client {
	return &Client{
		apiURL: apiURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		pinContent: pinContent,
	}
}

// ID returns the peer ID and other info about the IPFS node.
func (c *Client) ID(ctx context.Context) (*IDResponse, error) {
	resp, err := c.post(ctx, "/api/v0/id", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get node ID: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("IPFS API error: %s", string(body))
	}

	var result IDResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode ID response: %w", err)
	}

	return &result, nil
}

func recordIPFS(op string, start time.Time, err error) {
	result := "success"
	if err != nil {
		result = "error"
	}
	metrics.IPFSOperationsTotal.WithLabelValues(op, result).Inc()
	metrics.IPFSOperationDuration.WithLabelValues(op).Observe(time.Since(start).Seconds())
}

// Add adds content to IPFS and returns the CID.
func (c *Client) Add(ctx context.Context, r io.Reader) (_ *AddResponse, err error) {
	start := time.Now()
	defer func() { recordIPFS("add", start, err) }()

	// Create multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", "blob")
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}

	if _, err = io.Copy(part, r); err != nil {
		return nil, fmt.Errorf("failed to copy content: %w", err)
	}

	if err = writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	// Build URL with query params
	params := url.Values{}
	params.Set("pin", fmt.Sprintf("%t", c.pinContent))
	params.Set("cid-version", "1") // Use CIDv1 for better compatibility
	params.Set("hash", "sha2-256")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/api/v0/add?%s", c.apiURL, params.Encode()), &buf)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to add to IPFS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("IPFS add failed: %s", string(body))
	}

	var result AddResponse
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode add response: %w", err)
	}

	return &result, nil
}

// Cat retrieves content from IPFS by CID.
func (c *Client) Cat(ctx context.Context, cid string) (_ io.ReadCloser, err error) {
	start := time.Now()
	defer func() { recordIPFS("cat", start, err) }()

	params := url.Values{}
	params.Set("arg", cid)

	resp, err := c.post(ctx, "/api/v0/cat", params, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to cat from IPFS: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("IPFS cat failed: %s", string(body))
	}

	return resp.Body, nil
}

// Pin pins a CID to prevent garbage collection.
func (c *Client) Pin(ctx context.Context, cid string) (err error) {
	start := time.Now()
	defer func() { recordIPFS("pin", start, err) }()

	params := url.Values{}
	params.Set("arg", cid)

	resp, err := c.post(ctx, "/api/v0/pin/add", params, nil)
	if err != nil {
		return fmt.Errorf("failed to pin: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("IPFS pin failed: %s", string(body))
	}

	return nil
}

// Unpin unpins a CID.
func (c *Client) Unpin(ctx context.Context, cid string) (err error) {
	start := time.Now()
	defer func() { recordIPFS("unpin", start, err) }()

	params := url.Values{}
	params.Set("arg", cid)

	resp, err := c.post(ctx, "/api/v0/pin/rm", params, nil)
	if err != nil {
		return fmt.Errorf("failed to unpin: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("IPFS unpin failed: %s", string(body))
	}

	return nil
}

// Stat returns information about a CID without fetching the content.
func (c *Client) Stat(ctx context.Context, cid string) (_ int64, err error) {
	start := time.Now()
	defer func() { recordIPFS("stat", start, err) }()

	params := url.Values{}
	params.Set("arg", cid)

	resp, err := c.post(ctx, "/api/v0/object/stat", params, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to stat: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("IPFS stat failed: %s", string(body))
	}

	var result struct {
		CumulativeSize int64 `json:"CumulativeSize"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("failed to decode stat response: %w", err)
	}

	return result.CumulativeSize, nil
}

// PubsubSubscribe subscribes to a pubsub topic and returns a channel of messages.
func (c *Client) PubsubSubscribe(ctx context.Context, topic string) (<-chan PubsubMessage, error) {
	params := url.Values{}
	// Kubo requires multibase encoding: 'u' prefix + base64url without padding
	encodedTopic := "u" + base64.RawURLEncoding.EncodeToString([]byte(topic))
	params.Set("arg", encodedTopic)

	// Use a longer timeout client for pubsub (it's a long-lived connection)
	pubsubClient := &http.Client{
		Timeout: 0, // No timeout for streaming
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/api/v0/pubsub/sub?%s", c.apiURL, params.Encode()), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create pubsub request: %w", err)
	}

	resp, err := pubsubClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("pubsub subscribe failed: %s", string(body))
	}

	messages := make(chan PubsubMessage, 100)

	go func() {
		defer resp.Body.Close()
		defer close(messages)

		decoder := json.NewDecoder(resp.Body)
		for {
			var msg PubsubMessage
			if err := decoder.Decode(&msg); err != nil {
				if ctx.Err() != nil {
					return // Context cancelled
				}
				// Connection closed or error
				return
			}

			select {
			case messages <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()

	return messages, nil
}

// PubsubPublish publishes a message to a pubsub topic.
func (c *Client) PubsubPublish(ctx context.Context, topic string, data []byte) error {
	params := url.Values{}
	// Kubo requires multibase encoding: 'u' prefix + base64url without padding
	encodedTopic := "u" + base64.RawURLEncoding.EncodeToString([]byte(topic))
	params.Set("arg", encodedTopic)

	// Create multipart form for the data
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", "data")
	if err != nil {
		return fmt.Errorf("failed to create form: %w", err)
	}

	if _, err := part.Write(data); err != nil {
		return fmt.Errorf("failed to write data: %w", err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("failed to close form: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/api/v0/pubsub/pub?%s", c.apiURL, params.Encode()), &buf)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to publish: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pubsub publish failed: %s", string(body))
	}

	return nil
}

// post is a helper for POST requests to the IPFS API.
func (c *Client) post(ctx context.Context, path string, params url.Values, body io.Reader) (*http.Response, error) {
	urlStr := c.apiURL + path
	if params != nil {
		urlStr += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, body)
	if err != nil {
		return nil, err
	}

	return c.httpClient.Do(req)
}

// IsAvailable checks if the IPFS node is reachable.
func (c *Client) IsAvailable(ctx context.Context) bool {
	_, err := c.ID(ctx)
	return err == nil
}
