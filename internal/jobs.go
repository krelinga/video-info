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
