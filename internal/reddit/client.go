package reddit

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultAPIBase = "https://oauth.reddit.com"
	DefaultWWWBase = "https://www.reddit.com"

	requestTimeout = 15 * time.Second
	maxRateWait    = 5 * time.Second
	retryBackoff   = 500 * time.Millisecond
	tokenSlack     = time.Minute
)

// Config configures a Client. Only ClientID, ClientSecret and UserAgent are required.
type Config struct {
	ClientID     string
	ClientSecret string
	UserAgent    string

	APIBase    string // default DefaultAPIBase
	WWWBase    string // default DefaultWWWBase; token endpoint and share links
	HTTPClient *http.Client
	Now        func() time.Time
	Sleep      func(context.Context, time.Duration) error
}

// Client talks to Reddit's OAuth API with application-only credentials.
type Client struct {
	cfg        Config
	http       *http.Client
	noRedirect *http.Client

	tokenMu  sync.Mutex
	token    string
	tokenExp time.Time

	rateMu        sync.Mutex
	rateKnown     bool
	rateRemaining float64
	rateReset     time.Time
}

// New returns a Client, filling in defaults for unset Config fields.
func New(cfg Config) *Client {
	if cfg.APIBase == "" {
		cfg.APIBase = DefaultAPIBase
	}
	if cfg.WWWBase == "" {
		cfg.WWWBase = DefaultWWWBase
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleepCtx
	}
	noRedirect := *cfg.HTTPClient
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{cfg: cfg, http: cfg.HTTPClient, noRedirect: &noRedirect}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// get performs an authenticated GET on the API and decodes the JSON body into out.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if q == nil {
		q = url.Values{}
	}
	q.Set("raw_json", "1")
	endpoint := c.cfg.APIBase + path + "?" + q.Encode()

	authRetried, serverRetried := false, false
	for {
		resp, err := c.doAPI(ctx, endpoint)
		if err != nil {
			return err
		}
		switch {
		case resp.StatusCode == http.StatusUnauthorized && !authRetried:
			discard(resp)
			c.invalidateToken()
			authRetried = true
			continue
		case resp.StatusCode == http.StatusUnauthorized:
			discard(resp)
			return &AuthError{Status: resp.StatusCode}
		case resp.StatusCode == http.StatusTooManyRequests:
			discard(resp)
			return &RateLimitError{RetryAfter: retryAfter(resp.Header)}
		case resp.StatusCode >= 500 && !serverRetried:
			discard(resp)
			serverRetried = true
			if err := c.cfg.Sleep(ctx, retryBackoff); err != nil {
				return err
			}
			continue
		}
		return decodeResponse(resp, out)
	}
}

func (c *Client) doAPI(ctx context.Context, endpoint string) (*http.Response, error) {
	if err := c.waitRateLimit(ctx); err != nil {
		return nil, err
	}
	tok, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	// Redirects are not followed: Reddit redirects searches in a nonexistent
	// subreddit to a subreddit search, which would decode as "no posts".
	resp, err := c.noRedirect.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reddit request failed: %w", err)
	}
	c.updateRateLimit(resp.Header)
	return resp, nil
}

func decodeResponse(resp *http.Response, out any) error {
	defer discard(resp)
	switch {
	case resp.StatusCode == http.StatusForbidden:
		return ErrForbidden
	case resp.StatusCode == http.StatusNotFound,
		resp.StatusCode >= 300 && resp.StatusCode < 400:
		return ErrNotFound
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return &StatusError{Status: resp.StatusCode}
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "json") {
		return ErrUnexpectedResponse
	}
	if err := json.UnmarshalRead(resp.Body, out, jsonOpts); err != nil {
		return fmt.Errorf("decoding reddit response: %w", err)
	}
	return nil
}

// discard drains and closes the body so the connection can be reused.
func discard(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.token != "" && c.cfg.Now().Before(c.tokenExp.Add(-tokenSlack)) {
		return c.token, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.WWWBase+"/api/v1/access_token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(c.cfg.ClientID, c.cfg.ClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("requesting reddit access token: %w", err)
	}
	defer discard(resp)
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", &AuthError{Status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK {
		return "", &StatusError{Status: resp.StatusCode}
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}
	if err := json.UnmarshalRead(resp.Body, &tr, jsonOpts); err != nil {
		return "", fmt.Errorf("decoding reddit access token: %w", err)
	}
	if tr.AccessToken == "" {
		return "", &AuthError{Status: resp.StatusCode, Reason: tr.Error}
	}
	c.token = tr.AccessToken
	c.tokenExp = c.cfg.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return c.token, nil
}

func (c *Client) invalidateToken() {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	c.token = ""
}

func (c *Client) updateRateLimit(h http.Header) {
	remaining, err1 := strconv.ParseFloat(h.Get("X-Ratelimit-Remaining"), 64)
	reset, err2 := strconv.ParseFloat(h.Get("X-Ratelimit-Reset"), 64)
	if err1 != nil || err2 != nil {
		return
	}
	c.rateMu.Lock()
	defer c.rateMu.Unlock()
	c.rateKnown = true
	c.rateRemaining = remaining
	c.rateReset = c.cfg.Now().Add(time.Duration(reset * float64(time.Second)))
}

// waitRateLimit sleeps through a short reset window and fails fast on a long one.
func (c *Client) waitRateLimit(ctx context.Context) error {
	c.rateMu.Lock()
	known, remaining, reset := c.rateKnown, c.rateRemaining, c.rateReset
	c.rateMu.Unlock()
	if !known || remaining >= 1 {
		return nil
	}
	d := reset.Sub(c.cfg.Now())
	if d <= 0 {
		return nil
	}
	if d > maxRateWait {
		return &RateLimitError{RetryAfter: d}
	}
	return c.cfg.Sleep(ctx, d)
}

func retryAfter(h http.Header) time.Duration {
	for _, key := range []string{"X-Ratelimit-Reset", "Retry-After"} {
		if s, err := strconv.ParseFloat(h.Get(key), 64); err == nil && s > 0 {
			return time.Duration(s * float64(time.Second))
		}
	}
	return time.Minute
}
