package cdn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/louis-bourgault/hccdn-cli/types"
)

const DefaultBaseURL = "https://cdn.hackclub.com/api/v4"

type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
	Retries    int
}

type Payload struct {
	Filename string
	Open     func() (io.ReadCloser, error)
}

type Account struct {
	ID           string `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	StorageUsed  int64  `json:"storage_used"`
	StorageLimit int64  `json:"storage_limit"`
	QuotaTier    string `json:"quota_tier"`
}

type StatusError struct {
	StatusCode int
	Message    string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("CDN returned HTTP %d: %s", e.StatusCode, e.Message)
}

func NewClient(apiKey, baseURL string, timeout time.Duration, retries int) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if retries < 0 {
		retries = 0
	}
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		APIKey:     apiKey,
		HTTPClient: &http.Client{Timeout: timeout},
		Retries:    retries,
	}
}

func (c *Client) Upload(ctx context.Context, payload Payload) (*types.Upload, error) {
	var lastErr error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		if attempt > 0 {
			if err := wait(ctx, backoff(attempt)); err != nil {
				return nil, err
			}
		}
		upload, err := c.uploadOnce(ctx, payload)
		if err == nil {
			return upload, nil
		}
		lastErr = err
		if !retryable(err) {
			break
		}
	}
	return nil, lastErr
}

func (c *Client) uploadOnce(ctx context.Context, payload Payload) (*types.Upload, error) {
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	contentType := multipartWriter.FormDataContentType()
	writeDone := make(chan error, 1)
	go func() {
		file, err := payload.Open()
		if err != nil {
			writer.CloseWithError(err)
			writeDone <- err
			return
		}
		defer file.Close()
		part, err := multipartWriter.CreateFormFile("file", payload.Filename)
		if err == nil {
			_, err = io.Copy(part, file)
		}
		if closeErr := multipartWriter.Close(); err == nil {
			err = closeErr
		}
		writer.CloseWithError(err)
		writeDone <- err
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/upload", reader)
	if err != nil {
		reader.CloseWithError(err)
		<-writeDone
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	c.authorize(req)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		reader.CloseWithError(err)
		<-writeDone
		return nil, err
	}
	writeErr := <-writeDone
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, responseError(resp)
	}
	if writeErr != nil {
		return nil, writeErr
	}
	var upload types.Upload
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&upload); err != nil {
		return nil, fmt.Errorf("decode CDN upload response: %w", err)
	}
	if upload.ID == "" || upload.URL == "" {
		return nil, errors.New("CDN upload response is missing id or url")
	}
	return &upload, nil
}

func (c *Client) Delete(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("upload id is empty")
	}
	var lastErr error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		if attempt > 0 {
			if err := wait(ctx, backoff(attempt)); err != nil {
				return err
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.BaseURL+"/upload/"+id, nil)
		if err != nil {
			return err
		}
		c.authorize(req)
		resp, err := c.HTTPClient.Do(req)
		if err == nil {
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				resp.Body.Close()
				return nil
			}
			lastErr = responseError(resp)
			resp.Body.Close()
		} else {
			lastErr = err
		}
		if !retryable(lastErr) {
			break
		}
	}
	return lastErr
}

func (c *Client) Me(ctx context.Context) (*Account, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/me", nil)
	if err != nil {
		return nil, err
	}
	c.authorize(req)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, responseError(resp)
	}
	var account Account
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&account); err != nil {
		return nil, err
	}
	return &account, nil
}

// RemoteSHA256 hashes a legacy public CDN object so it can be safely associated
// with a source hash without trusting path or file size alone.
func (c *Client) RemoteSHA256(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", responseError(resp)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, resp.Body); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (c *Client) authorize(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("User-Agent", "hccdn-cli")
}

func responseError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	message := strings.TrimSpace(string(body))
	var parsed struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &parsed) == nil && parsed.Error != "" {
		message = parsed.Error
	}
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return &StatusError{StatusCode: resp.StatusCode, Message: message}
}

func retryable(err error) bool {
	var statusErr *StatusError
	if errors.As(err, &statusErr) {
		return statusErr.StatusCode == http.StatusTooManyRequests || statusErr.StatusCode == http.StatusBadGateway ||
			statusErr.StatusCode == http.StatusServiceUnavailable || statusErr.StatusCode == http.StatusGatewayTimeout
	}
	return true
}

func backoff(attempt int) time.Duration {
	delay := 250 * time.Millisecond * time.Duration(1<<(attempt-1))
	if delay > 4*time.Second {
		return 4 * time.Second
	}
	return delay
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
