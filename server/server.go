package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krelinga/video-info/internal"
	"github.com/krelinga/video-info/virest"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// Server implements the vtrest.StrictServerInterface for handling transcode requests.
type Server struct {
	pool        *pgxpool.Pool
	riverClient *river.Client[pgx.Tx]
}

// NewServer creates a new Server instance.
func NewServer(pool *pgxpool.Pool, riverClient *river.Client[pgx.Tx]) *Server {
	return &Server{
		pool:        pool,
		riverClient: riverClient,
	}
}

// CreateInfo handles POST /info requests.
func (s *Server) CreateInfo(ctx context.Context, request virest.CreateInfoRequestObject) (virest.CreateInfoResponseObject, error) {
	if request.Body == nil {
		return virest.CreateInfo400JSONResponse{
			Code:    "INVALID_REQUEST",
			Message: "Request body is required",
		}, nil
	}

	jobArgs := internal.InfoJobArgs{
		UUID:         uuid.UUID(request.Body.Uuid),
		Path:         request.Body.VideoPath,
		WebhookURI:   request.Body.WebhookUri,
		WebhookToken: request.Body.WebhookToken,
	}

	// Use a transaction to insert job and mapping atomically
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return virest.CreateInfo500JSONResponse{
			Code:    "INTERNAL_ERROR",
			Message: fmt.Sprintf("failed to begin transaction: %v", err),
		}, nil
	}
	defer tx.Rollback(ctx)

	// Check if UUID already exists
	var existingJobID int64
	err = tx.QueryRow(ctx, "SELECT river_job_id FROM uuid_job_mapping WHERE uuid = $1", jobArgs.UUID).Scan(&existingJobID)
	if err == nil {
		// UUID already exists
		return virest.CreateInfo409JSONResponse{
			Code:    "DUPLICATE_UUID",
			Message: fmt.Sprintf("An info job with UUID %s already exists", jobArgs.UUID),
		}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return virest.CreateInfo500JSONResponse{
			Code:    "INTERNAL_ERROR",
			Message: fmt.Sprintf("failed to check existing UUID: %v", err),
		}, nil
	}

	// Insert job into River
	insertedJob, err := s.riverClient.InsertTx(ctx, tx, jobArgs, nil)
	if err != nil {
		return virest.CreateInfo500JSONResponse{
			Code:    "INTERNAL_ERROR",
			Message: fmt.Sprintf("failed to insert river job: %v", err),
		}, nil
	}

	// Insert UUID to job ID mapping
	_, err = tx.Exec(ctx, "INSERT INTO uuid_job_mapping (uuid, river_job_id) VALUES ($1, $2)", jobArgs.UUID, insertedJob.Job.ID)
	if err != nil {
		return virest.CreateInfo500JSONResponse{
			Code:    "INTERNAL_ERROR",
			Message: fmt.Sprintf("failed to insert uuid mapping: %v", err),
		}, nil
	}

	if err := tx.Commit(ctx); err != nil {
		return virest.CreateInfo500JSONResponse{
			Code:    "INTERNAL_ERROR",
			Message: fmt.Sprintf("failed to commit transaction: %v", err),
		}, nil
	}

	now := time.Now()
	return virest.CreateInfo201JSONResponse{
		Uuid:      request.Body.Uuid,
		Status:    virest.Pending,
		VideoPath: request.Body.VideoPath,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// GetInfoStatus handles GET /info/{uuid} requests.
func (s *Server) GetInfoStatus(ctx context.Context, request virest.GetInfoStatusRequestObject) (virest.GetInfoStatusResponseObject, error) {
	// Look up river job ID from UUID
	var riverJobID int64
	err := s.pool.QueryRow(ctx, "SELECT river_job_id FROM uuid_job_mapping WHERE uuid = $1", request.Uuid).Scan(&riverJobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return virest.GetInfoStatus404JSONResponse{
			Code:    "NOT_FOUND",
			Message: fmt.Sprintf("Info job with UUID %s not found", request.Uuid),
		}, nil
	} else if err != nil {
		return virest.GetInfoStatus500JSONResponse{
			Code:    "INTERNAL_ERROR",
			Message: fmt.Sprintf("failed to look up job mapping: %v", err),
		}, nil
	}

	// Get job from River
	job, err := s.riverClient.JobGet(ctx, riverJobID)
	if err != nil {
		return virest.GetInfoStatus500JSONResponse{
			Code:    "INTERNAL_ERROR",
			Message: fmt.Sprintf("failed to get river job: %v", err),
		}, nil
	}
	if job == nil {
		return virest.GetInfoStatus404JSONResponse{
			Code:    "NOT_FOUND",
			Message: fmt.Sprintf("Info job with UUID %s not found in queue", request.Uuid),
		}, nil
	}

	// Parse job args for source/destination paths
	var jobArgs internal.InfoJobArgs
	if err := json.Unmarshal(job.EncodedArgs, &jobArgs); err != nil {
		return virest.GetInfoStatus500JSONResponse{
			Code:    "INTERNAL_ERROR",
			Message: fmt.Sprintf("failed to unmarshal job args: %v", err),
		}, nil
	}

	// Parse job output for progress/error if present
	var jobStatus internal.InfoJobStatus
	jobOutput := job.Output()
	if len(jobOutput) > 0 {
		if err := json.Unmarshal(jobOutput, &jobStatus); err != nil {
			return virest.GetInfoStatus500JSONResponse{
				Code:    "INTERNAL_ERROR",
				Message: fmt.Sprintf("failed to unmarshal job output: %v", err),
			}, nil
		}
	}

	// Map River state to TranscodeStatus
	status := mapRiverStateToTranscodeStatus(job.State)

	// Use job error if status is failed and no output error
	var jobError *string
	if jobStatus.Error != nil {
		jobError = jobStatus.Error
	} else if status == virest.Failed && len(job.Errors) > 0 {
		lastError := job.Errors[len(job.Errors)-1].Error
		jobError = &lastError
	}

	finalTime := job.CreatedAt
	if job.FinalizedAt != nil {
		finalTime = *job.FinalizedAt
	}
	var result *virest.VideoInfo
	if jobStatus.Result != nil {
		result = jobStatus.Result.RESTVideoInfo()
	}
	return virest.GetInfoStatus200JSONResponse{
		Uuid:      request.Uuid,
		Status:    status,
		VideoPath: jobArgs.Path,
		Result:    result,
		Error:     jobError,
		CreatedAt: job.CreatedAt.UTC(),
		UpdatedAt: finalTime.UTC(),
	}, nil
}

// mapRiverStateToTranscodeStatus converts River job state to API TranscodeStatus.
func mapRiverStateToTranscodeStatus(state rivertype.JobState) virest.InfoStatus {
	switch state {
	case rivertype.JobStateAvailable, rivertype.JobStateScheduled, rivertype.JobStateRetryable, rivertype.JobStatePending:
		return virest.Pending
	case rivertype.JobStateRunning:
		return virest.Running
	case rivertype.JobStateCompleted:
		return virest.Completed
	case rivertype.JobStateDiscarded, rivertype.JobStateCancelled:
		return virest.Failed
	default:
		return virest.Pending
	}
}
