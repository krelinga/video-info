package internal

import (
	"github.com/google/uuid"
	"github.com/krelinga/video-info/virest"
)

// InfoJobArgs contains the arguments for an info job.
// This is used as the River job args payload.
type InfoJobArgs struct {
	UUID         uuid.UUID `json:"uuid"`
	Path         string    `json:"path"`
	WebhookURI   *string   `json:"webhook_uri,omitempty"`
	WebhookToken []byte    `json:"webhook_token,omitempty"`
}

// Kind returns the job kind identifier for River.
func (InfoJobArgs) Kind() string {
	return "info"
}

type InfoJobResult struct {
	DurationSeconds         float64   `json:"duration_seconds"`
	ChapterDurationsSeconds []float64 `json:"chapter_durations_seconds"`
}

func (r *InfoJobResult) RESTVideoInfo() *virest.VideoInfo {
	if r == nil {
		return nil
	}
	return &virest.VideoInfo{
		TotalDurationSeconds:    r.DurationSeconds,
		ChapterDurationsSeconds: r.ChapterDurationsSeconds,
	}
}

type InfoJobStatus struct {
	Error  *string        `json:"error,omitempty"`
	Result *InfoJobResult `json:"result,omitempty"`
}

// WebhookJobArgs contains the arguments for a webhook notification job.
type WebhookJobArgs struct {
	URI    string         `json:"uri"`
	Token  []byte         `json:"token,omitempty"`
	Uuid   uuid.UUID      `json:"info_uuid"`
	Status *InfoJobStatus `json:"status,omitempty"`
}

// Kind returns the job kind identifier for River.
func (WebhookJobArgs) Kind() string {
	return "webhook"
}
