// SPDX-License-Identifier: Apache-2.0

package enroll

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/inceptionstack/telemetron/internal/installid"
)

const (
	enrollSchema        = "lowkey.enroll.v1"
	defaultHTTPTimeout  = 10 * time.Second
	maxRetryJitter      = 500 * time.Millisecond
	maxResponseBodyRead = 4096
)

var (
	// install_id format validation is delegated to installid.Validate so
	// there's a single source of truth for UUIDv4 acceptance across the
	// client.
	machineIDRe   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	enrollTokenRe = regexp.MustCompile(`^lpk_enroll_[0-9a-f]{64}$`)

	ErrConflict = errors.New("install_id already enrolled to a different machine")
)

// Client posts enrollment requests to the telemetron enrollment endpoint.
type Client struct {
	endpoint   string
	httpClient *http.Client
}

type EnrollRequest struct {
	InstallID         string
	MachineID         string
	OS                string
	Arch              string
	Source            string
	TelemetronVersion string
}

type EnrollResponse struct {
	Token     string
	InstallID string
}

type retryableError struct {
	err error
}

func (e *retryableError) Error() string {
	return "retryable enrollment failure: " + e.err.Error()
}

func (e *retryableError) Unwrap() error {
	return e.err
}

func (e *retryableError) Retryable() bool {
	return true
}

func NewClient(endpoint string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	} else {
		clone := *httpClient
		if clone.Timeout == 0 {
			clone.Timeout = defaultHTTPTimeout
		}
		httpClient = &clone
	}
	return &Client{
		endpoint:   endpoint,
		httpClient: httpClient,
	}
}

func (c *Client) Enroll(ctx context.Context, req EnrollRequest) (EnrollResponse, error) {
	if err := validateRequest(req); err != nil {
		return EnrollResponse{}, err
	}

	payload, err := json.Marshal(enrollRequestPayload{
		Schema:            enrollSchema,
		InstallID:         req.InstallID,
		MachineID:         req.MachineID,
		OS:                req.OS,
		Arch:              req.Arch,
		Source:            req.Source,
		TelemetronVersion: req.TelemetronVersion,
	})
	if err != nil {
		return EnrollResponse{}, fmt.Errorf("marshal enroll request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := c.do(ctx, payload)
		if err == nil {
			parsed, parseErr := parseResponse(resp)
			if parseErr == nil {
				return parsed, nil
			}
			if errors.Is(parseErr, ErrConflict) {
				return EnrollResponse{}, parseErr
			}
			if !isRetryable(parseErr) {
				return EnrollResponse{}, parseErr
			}
			lastErr = parseErr
		} else {
			if ctx.Err() != nil {
				return EnrollResponse{}, err
			}
			lastErr = err
		}

		if attempt == 1 {
			break
		}
		if sleepErr := sleepWithJitter(ctx); sleepErr != nil {
			return EnrollResponse{}, sleepErr
		}
	}

	return EnrollResponse{}, &retryableError{err: lastErr}
}

type enrollRequestPayload struct {
	Schema            string `json:"schema"`
	InstallID         string `json:"install_id"`
	MachineID         string `json:"machine_id"`
	OS                string `json:"os"`
	Arch              string `json:"arch"`
	Source            string `json:"source"`
	TelemetronVersion string `json:"telemetron_version"`
}

type enrollResponsePayload struct {
	Token     string `json:"token"`
	InstallID string `json:"install_id"`
}

func validateRequest(req EnrollRequest) error {
	if !installid.Validate(req.InstallID) {
		return fmt.Errorf("invalid install_id %q", req.InstallID)
	}
	if !machineIDRe.MatchString(req.MachineID) {
		return fmt.Errorf("invalid machine_id %q", req.MachineID)
	}
	return nil
}

func (c *Client) do(ctx context.Context, payload []byte) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build enroll request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func parseResponse(resp *http.Response) (EnrollResponse, error) {
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyRead))
	if err != nil {
		return EnrollResponse{}, fmt.Errorf("read enroll response: %w", err)
	}
	bodyText := strings.TrimSpace(string(body))

	switch {
	case resp.StatusCode == http.StatusConflict:
		return EnrollResponse{}, fmt.Errorf("%w", ErrConflict)
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return EnrollResponse{}, fmt.Errorf("enroll request failed: status %d: %s", resp.StatusCode, bodyText)
	case resp.StatusCode >= 500:
		return EnrollResponse{}, fmt.Errorf("retryable server error: status %d: %s", resp.StatusCode, bodyText)
	case resp.StatusCode != http.StatusOK:
		return EnrollResponse{}, fmt.Errorf("unexpected enroll response: status %d: %s", resp.StatusCode, bodyText)
	}

	var payloadResp enrollResponsePayload
	if err := json.Unmarshal(body, &payloadResp); err != nil {
		return EnrollResponse{}, fmt.Errorf("decode enroll response: %w", err)
	}
	if !enrollTokenRe.MatchString(payloadResp.Token) {
		return EnrollResponse{}, fmt.Errorf("malformed enroll token %q", payloadResp.Token)
	}
	if !installid.Validate(payloadResp.InstallID) {
		return EnrollResponse{}, fmt.Errorf("malformed install_id %q", payloadResp.InstallID)
	}

	return EnrollResponse(payloadResp), nil
}

func isRetryable(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "retryable") || strings.Contains(err.Error(), "server error"))
}

func sleepWithJitter(ctx context.Context) error {
	jitter, err := randomJitter(maxRetryJitter)
	if err != nil {
		return fmt.Errorf("compute retry jitter: %w", err)
	}
	timer := time.NewTimer(jitter)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func randomJitter(max time.Duration) (time.Duration, error) {
	if max <= 0 {
		return 0, nil
	}
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	n := int(b[0])<<8 | int(b[1])
	return time.Duration(n%int(max.Milliseconds()+1)) * time.Millisecond, nil
}
