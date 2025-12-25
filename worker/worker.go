package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krelinga/video-info/internal"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// ffprobeOutput represents the JSON output from ffprobe.
type ffprobeOutput struct {
	Format   ffprobeFormat    `json:"format"`
	Chapters []ffprobeChapter `json:"chapters"`
}

type ffprobeFormat struct {
	Duration string `json:"duration"`
}

type ffprobeChapter struct {
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
}

// InfoWorker handles video information extraction jobs.
type InfoWorker struct {
	river.WorkerDefaults[internal.InfoJobArgs]
	DBPool *pgxpool.Pool
}

// Work executes the video info extraction job using ffprobe.
func (w *InfoWorker) Work(ctx context.Context, job *river.Job[internal.InfoJobArgs]) error {
	status := internal.InfoJobStatus{}

	result, err := extractVideoInfo(ctx, job.Args.Path)
	if err != nil {
		errMsg := err.Error()
		status.Error = &errMsg
	} else {
		status.Result = result
	}

	if err := river.RecordOutput(ctx, status); err != nil {
		return fmt.Errorf("failed to record output: %w", err)
	}

	// Enqueue webhook job if webhook URI is configured
	if job.Args.WebhookURI != nil {
		webhookArgs := internal.WebhookJobArgs{
			URI:    *job.Args.WebhookURI,
			Token:  job.Args.WebhookToken,
			Uuid:   job.Args.UUID,
			Status: &status,
		}

		// Start a transaction to insert webhook job and complete info job atomically
		tx, err := w.DBPool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}
		defer tx.Rollback(ctx)

		client := river.ClientFromContext[pgx.Tx](ctx)
		if client == nil {
			return fmt.Errorf("no river client in context for webhook job insertion")
		}

		if _, err := client.InsertTx(ctx, tx, webhookArgs, nil); err != nil {
			return fmt.Errorf("failed to enqueue webhook job: %w", err)
		}

		// Complete the current job within the same transaction
		if _, err := river.JobCompleteTx[*riverpgxv5.Driver](ctx, tx, job); err != nil {
			return fmt.Errorf("failed to complete job in transaction: %w", err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("failed to commit transaction: %w", err)
		}
	}

	return nil
}

// extractVideoInfo uses ffprobe to extract video duration and chapter information.
func extractVideoInfo(ctx context.Context, videoPath string) (*internal.InfoJobResult, error) {
	// Run ffprobe to get format and chapter information in JSON format
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_chapters",
		videoPath,
	)

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("ffprobe failed: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("failed to run ffprobe: %w", err)
	}

	var probeResult ffprobeOutput
	if err := json.Unmarshal(output, &probeResult); err != nil {
		return nil, fmt.Errorf("failed to parse ffprobe output: %w", err)
	}

	// Parse total duration
	var totalDuration float64
	if _, err := fmt.Sscanf(probeResult.Format.Duration, "%f", &totalDuration); err != nil {
		return nil, fmt.Errorf("failed to parse duration: %w", err)
	}

	// Parse chapter durations
	chapterDurations := make([]float64, 0, len(probeResult.Chapters))
	for _, chapter := range probeResult.Chapters {
		var startTime, endTime float64
		if _, err := fmt.Sscanf(chapter.StartTime, "%f", &startTime); err != nil {
			return nil, fmt.Errorf("failed to parse chapter start time: %w", err)
		}
		if _, err := fmt.Sscanf(chapter.EndTime, "%f", &endTime); err != nil {
			return nil, fmt.Errorf("failed to parse chapter end time: %w", err)
		}
		chapterDurations = append(chapterDurations, endTime-startTime)
	}

	return &internal.InfoJobResult{
		DurationSeconds:         totalDuration,
		ChapterDurationsSeconds: chapterDurations,
	}, nil
}
