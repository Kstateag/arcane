package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/getarcaneapp/arcane/backend/v2/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildGotifyEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		config   models.GotifyConfig
		wantErr  bool
		expected string
	}{
		{
			name: "basic HTTPS config",
			config: models.GotifyConfig{
				Host:  "gotify.example.com",
				Token: "A12345678901234",
			},
			expected: "https://gotify.example.com/message",
		},
		{
			name: "config with port",
			config: models.GotifyConfig{
				Host:  "gotify.example.com",
				Port:  8443,
				Token: "A12345678901234",
			},
			expected: "https://gotify.example.com:8443/message",
		},
		{
			name: "config with path",
			config: models.GotifyConfig{
				Host:  "gotify.example.com",
				Path:  "/gotify/",
				Token: "A12345678901234",
			},
			expected: "https://gotify.example.com/gotify/message",
		},
		{
			name: "HTTP when TLS disabled",
			config: models.GotifyConfig{
				Host:       "gotify.example.com",
				Port:       80,
				Path:       "gotify",
				Token:      "gtfya.synthetic-regression-token-not-a-secret",
				DisableTLS: true,
			},
			expected: "http://gotify.example.com:80/gotify/message",
		},
		{
			name: "missing host",
			config: models.GotifyConfig{
				Token: "A12345678901234",
			},
			wantErr: true,
		},
		{
			name: "missing token",
			config: models.GotifyConfig{
				Host: "gotify.example.com",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoint, err := buildGotifyEndpointInternal(tt.config)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, endpoint)
		})
	}
}

func TestSendGotifySupportsGotifyV3Token(t *testing.T) {
	const token = "gtfya.synthetic-regression-token-not-a-secret"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/gotify/message", r.URL.Path)
		assert.Equal(t, token, r.Header.Get("X-Gotify-Key"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var payload gotifyMessageRequest
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&payload)) {
			http.Error(w, "invalid request payload", http.StatusBadRequest)
			return
		}

		assert.Equal(t, "Arcane test notification", payload.Message)
		assert.Equal(t, "Arcane", payload.Title)
		assert.Equal(t, 5, payload.Priority)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer server.Close()

	config := gotifyConfigForTestServer(t, server.URL)
	config.Path = "/gotify"
	config.Token = token
	config.Title = "Arcane"
	config.Priority = 5

	err := SendGotify(context.Background(), config, "Arcane test notification")
	require.NoError(t, err)
}

func TestSendGotifyRejectsCrossOriginRedirect(t *testing.T) {
	targetRequests := make(chan string, 1)

	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetRequests <- r.Header.Get("X-Gotify-Key")
		w.WriteHeader(http.StatusOK)
	}))
	defer targetServer.Close()

	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetServer.URL+"/stolen", http.StatusTemporaryRedirect)
	}))
	defer redirectServer.Close()

	config := gotifyConfigForTestServer(t, redirectServer.URL)
	config.Token = "gtfya.synthetic-regression-token-not-a-secret"

	err := SendGotify(context.Background(), config, "test message")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing Gotify redirect to a different origin")

	select {
	case token := <-targetRequests:
		t.Fatalf("cross-origin redirect target received Gotify token %q", token)
	default:
	}
}

func TestSendGotifyReturnsServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	config := gotifyConfigForTestServer(t, server.URL)
	config.Token = "gtfya.invalid"

	err := SendGotify(context.Background(), config, "test message")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gotify returned HTTP 401")
	assert.Contains(t, err.Error(), "invalid token")
}

func gotifyConfigForTestServer(t *testing.T, serverURL string) models.GotifyConfig {
	t.Helper()

	parsed, err := url.Parse(serverURL)
	require.NoError(t, err)

	host, portString, err := net.SplitHostPort(parsed.Host)
	require.NoError(t, err)

	port, err := strconv.Atoi(portString)
	require.NoError(t, err)

	return models.GotifyConfig{
		Host:       host,
		Port:       port,
		Token:      fmt.Sprintf("test-token-%d", port),
		DisableTLS: true,
	}
}

func TestSendGotifyRejectsEmptyMessage(t *testing.T) {
	err := SendGotify(context.Background(), models.GotifyConfig{
		Host:  "gotify.example.com",
		Token: "gtfya.synthetic-regression-token-not-a-secret",
	}, "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "gotify message is required")
}
