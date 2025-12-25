package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/krelinga/video-info/internal"
	"github.com/riverqueue/river"
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
