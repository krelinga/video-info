package main

import (
	"context"

	"github.com/krelinga/video-info/internal"
	"github.com/riverqueue/river"
)

// InfoWorker handles video information extraction jobs.
type InfoWorker struct {
	river.WorkerDefaults[internal.InfoJobArgs]
}

// Work executes the transcoding job using HandBrake CLI.
func (w *InfoWorker) Work(ctx context.Context, job *river.Job[internal.InfoJobArgs]) error {
	// TODO: implement
	return nil
}
