package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// APIError represents a non-2xx response from the coordinator.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("api: %d — %s", e.StatusCode, e.Message)
}

// Client talks to the siliconrig coordinator API.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// New creates a client with sensible defaults.
func New(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// do performs an HTTP request with auth and JSON encoding.
func (c *Client) do(method, path string, body, result any) error {
	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}

	req, err := http.NewRequest(method, c.BaseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	if c.APIKey != "" {
		req.Header.Set("X-API-Key", c.APIKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &errResp) == nil && errResp.Error != "" {
			return &APIError{StatusCode: resp.StatusCode, Message: errResp.Error}
		}
		return &APIError{StatusCode: resp.StatusCode, Message: string(raw)}
	}

	if result != nil {
		if err := json.Unmarshal(raw, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// ListBoards returns all boards (public, no auth required).
func (c *Client) ListBoards() ([]Board, error) {
	var boards []Board
	if err := c.do("GET", "/v1/boards", nil, &boards); err != nil {
		return nil, err
	}
	return boards, nil
}

// Config is the subset of platform settings a client needs to behave
// correctly, from GET /v1/config.
type Config struct {
	IdleTimeoutSeconds int `json:"idle_timeout_seconds"`
}

// GetConfig returns the platform settings. Callers treat a failure as unknown
// rather than fatal: it only shapes what the CLI prints.
func (c *Client) GetConfig() (*Config, error) {
	var cfg Config
	if err := c.do("GET", "/v1/config", nil, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// CreateSession starts a new session for the given board type.
func (c *Client) CreateSession(boardType string) (*Session, error) {
	body := map[string]string{"board_type": boardType}
	var sess Session
	if err := c.do("POST", "/v1/sessions", body, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

// SessionsPage is the paginated response from GET /v1/sessions.
type SessionsPage struct {
	Active []Session `json:"active"`
	Ended  []Session `json:"ended"`
	Total  int       `json:"total"`
	Limit  int       `json:"limit"`
	Offset int       `json:"offset"`
}

// ListSessions returns the authenticated user's sessions (active + recent ended).
func (c *Client) ListSessions() (*SessionsPage, error) {
	var page SessionsPage
	if err := c.do("GET", "/v1/sessions", nil, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// GetSession returns a single session by ID.
func (c *Client) GetSession(id string) (*Session, error) {
	var sess Session
	if err := c.do("GET", "/v1/sessions/"+id, nil, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

// EndSession terminates a session by ID.
func (c *Client) EndSession(id string) (*Session, error) {
	var sess Session
	if err := c.do("DELETE", "/v1/sessions/"+id, nil, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

// GetMe returns the authenticated user's profile.
func (c *Client) GetMe() (*User, error) {
	var user User
	if err := c.do("GET", "/v1/auth/me", nil, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

// FlashFirmware uploads a firmware file to the given session.
func (c *Client) FlashFirmware(sessionID, filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("open firmware file: %w", err)
	}
	return c.FlashFirmwareBytes(sessionID, filepath.Base(filePath), data)
}

// FlashFirmwareBytes uploads in-memory firmware bytes to the given session.
func (c *Client) FlashFirmwareBytes(sessionID, name string, data []byte) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("firmware", name)
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return fmt.Errorf("write firmware: %w", err)
	}
	w.Close()

	req, err := http.NewRequest("POST", c.BaseURL+"/v1/sessions/"+sessionID+"/flash", &buf)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	if c.APIKey != "" {
		req.Header.Set("X-API-Key", c.APIKey)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &errResp) == nil && errResp.Error != "" {
			return &APIError{StatusCode: resp.StatusCode, Message: errResp.Error}
		}
		return &APIError{StatusCode: resp.StatusCode, Message: string(raw)}
	}
	return nil
}

// PowerCycle sends a power cycle command to the board in the given session.
func (c *Client) PowerCycle(sessionID string) error {
	return c.do("POST", "/v1/sessions/"+sessionID+"/power-cycle", nil, nil)
}

// DialSerialWS opens a WebSocket connection to the serial proxy for a session.
func (c *Client) DialSerialWS(ctx context.Context, sessionID string) (*websocket.Conn, error) {
	wsURL := c.BaseURL + "/v1/sessions/" + sessionID + "/serial"
	// Convert http(s) to ws(s)
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)
	wsURL = strings.Replace(wsURL, "http://", "ws://", 1)

	ws, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"X-API-Key": []string{c.APIKey},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("connect serial: %w", err)
	}
	return ws, nil
}

// FindActiveSession returns the first active/idle session, or an error.
func (c *Client) FindActiveSession() (*Session, error) {
	page, err := c.ListSessions()
	if err != nil {
		return nil, err
	}
	for i := range page.Active {
		s := &page.Active[i]
		if s.State == "active" || s.State == "idle" || s.State == "pending" ||
			s.State == "allocating" || s.State == "provisioning" {
			return s, nil
		}
	}
	return nil, fmt.Errorf("no active session found")
}

// IsTransient reports whether err is a transient failure worth retrying
// (server unavailability or a network-level error) rather than a client error
// (auth, validation, credits) that won't resolve on retry.
func IsTransient(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusServiceUnavailable, http.StatusInternalServerError,
			http.StatusBadGateway, http.StatusGatewayTimeout, http.StatusTooManyRequests:
			return true
		default:
			return false
		}
	}
	return true
}
