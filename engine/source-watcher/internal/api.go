package watcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const projectsPath = "/internal/v1/watcher/projects"

type project struct {
	ID               string  `json:"id"`
	RepoURL          string  `json:"repo_url"`
	ObservedSHA      *string `json:"observed_sha"`
	ObservedRevision *int64  `json:"observed_revision"`
}

type observation struct {
	PreviousSHA      *string `json:"previous_sha"`
	PreviousRevision int64   `json:"previous_revision"`
	SHA              string  `json:"sha"`
}

type Client struct {
	baseURL string
	token   string
	http    http.Client
}

func NewClient(baseURL, token string) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") ||
		u.Hostname() == "" || u.User != nil || strings.ContainsAny(baseURL, "?#") {
		return nil, errors.New("ENGINE_API_URL must be an HTTP or HTTPS URL without credentials, query or fragment")
	}
	if token == "" || strings.IndexFunc(token, func(r rune) bool { return r <= ' ' || r >= 127 }) != -1 {
		return nil, errors.New("ENGINE_WATCHER_TOKEN must contain nonempty printable ASCII without spaces")
	}

	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http: http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (c *Client) projects(ctx context.Context) ([]project, error) {
	resp, err := c.request(ctx, http.MethodGet, projectsPath, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var projects []project
	if err := json.NewDecoder(resp.Body).Decode(&projects); err != nil || projects == nil {
		return nil, errors.New("invalid projects response from Engine API")
	}
	return projects, nil
}

func (c *Client) observe(ctx context.Context, p project, sha string) error {
	body, err := json.Marshal(observation{
		PreviousSHA:      p.ObservedSHA,
		PreviousRevision: *p.ObservedRevision,
		SHA:              sha,
	})
	if err != nil {
		return err
	}
	resp, err := c.request(ctx, http.MethodPost, projectsPath+"/"+url.PathEscape(p.ID)+"/observations", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	if err != nil {
		return errors.New("could not read observation response from Engine API")
	}
	return nil
}

func (c *Client) request(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("could not build Engine API request")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, errors.New("Engine API request timed out")
		}
		return nil, errors.New("could not reach Engine API")
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("Engine API returned HTTP %d", resp.StatusCode)
	}
	return resp, nil
}
