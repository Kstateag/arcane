package notifications

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"emperror.dev/errors"

	"github.com/getarcaneapp/arcane/backend/v2/internal/models"
)

type gotifyMessageRequest struct {
	Message  string `json:"message"`
	Title    string `json:"title,omitempty"`
	Priority int    `json:"priority"`
}

// buildGotifyEndpointInternal converts GotifyConfig into the native Gotify message API endpoint.
func buildGotifyEndpointInternal(config models.GotifyConfig) (string, error) {
	if config.Host == "" {
		return "", errors.New("gotify host is required")
	}

	if config.Token == "" {
		return "", errors.New("gotify token is required")
	}

	scheme := "https"
	if config.DisableTLS {
		scheme = "http"
	}

	host := config.Host
	if config.Port > 0 {
		host = net.JoinHostPort(host, strconv.Itoa(config.Port))
	}

	path := strings.Trim(config.Path, "/")
	if path == "" {
		path = "/message"
	} else {
		path = "/" + path + "/message"
	}

	endpoint := &url.URL{
		Scheme: scheme,
		Host:   host,
		Path:   path,
	}

	return endpoint.String(), nil
}

// SendGotify sends a message directly through the Gotify HTTP API.
func SendGotify(ctx context.Context, config models.GotifyConfig, message string) error {
	if message == "" {
		return errors.New("gotify message is required")
	}

	endpoint, err := buildGotifyEndpointInternal(config)
	if err != nil {
		return errors.WrapIf(err, "failed to build gotify endpoint")
	}

	payload := gotifyMessageRequest{
		Message:  message,
		Title:    config.Title,
		Priority: config.Priority,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return errors.WrapIf(err, "failed to marshal gotify message")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.WrapIf(err, "failed to create gotify request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gotify-Key", config.Token)

	client := &http.Client{
		CheckRedirect: func(redirectReq *http.Request, via []*http.Request) error {
			if len(via) == 0 {
				return nil
			}

			originalURL := via[0].URL
			if redirectReq.URL.Scheme != originalURL.Scheme ||
				redirectReq.URL.Host != originalURL.Host {
				return errors.New("refusing Gotify redirect to a different origin")
			}

			return nil
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return errors.WrapIf(err, "failed to send gotify request")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if readErr != nil {
			return errors.WrapIf(readErr, "failed to read gotify error response")
		}

		detail := strings.TrimSpace(string(responseBody))
		if detail == "" {
			detail = http.StatusText(resp.StatusCode)
		}

		return fmt.Errorf("gotify returned HTTP %d: %s", resp.StatusCode, detail)
	}

	return nil
}
