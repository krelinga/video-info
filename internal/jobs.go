package internal

import "github.com/google/uuid"

// InfoJobArgs contains the arguments for an info job.
// This is used as the River job args payload.
type InfoJobArgs struct {
	UUID uuid.UUID `json:"uuid"`
	Path string    `json:"path"`
}

// Kind returns the job kind identifier for River.
func (InfoJobArgs) Kind() string {
	return "info"
}

type InfoJobResult struct {
	DurationSeconds float64 `json:"duration_seconds"`
	ChapterDurationsSeconds []float64 `json:"chapter_durations_seconds"`
}

type InfoJobStatus struct {
	Error *string `json:"error,omitempty"`
	Result *InfoJobResult `json:"result,omitempty"`
}