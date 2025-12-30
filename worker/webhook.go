package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/krelinga/video-info/internal"
	"github.com/krelinga/video-info/virest"
	"github.com/riverqueue/river"
)

// WebhookPayload is the JSON body sent to the webhook URI.
type WebhookPayload struct {
	Token  []byte            `json:"token,omitempty"`
	Uuid   uuid.UUID         `json:"uuid"`
	Result *virest.VideoInfo `json:"result,omitempty"`
	Error  *string           `json:"error,omitempty"`
}

// WebhookWorker handles webhook notification jobs.
type WebhookWorker struct {
	river.WorkerDefaults[internal.WebhookJobArgs]
	HTTPClient *http.Client
}

// Work executes the webhook notification job by POSTing to the configured URI.
func (w *WebhookWorker) Work(ctx context.Context, job *river.Job[internal.WebhookJobArgs]) error {
	payload := WebhookPayload{
		Token: job.Args.Token,
		Uuid:  job.Args.Uuid,
	}
	if job.Args.Status != nil {
		payload.Result = job.Args.Status.Result.RESTVideoInfo()
		payload.Error = job.Args.Status.Error
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, job.Args.URI, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := w.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	log.Printf("Sending webhook request to %q", req.URL.String())
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send webhook request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook request failed with status %d", resp.StatusCode)
	}

	return nil
}
