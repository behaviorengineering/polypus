package batch

import "strings"

// FilePurpose is the OpenAI Files API purpose string.
type FilePurpose string

const (
	FilePurposeBatch FilePurpose = "batch"
)

// FileRecord is OpenAI-shaped file metadata stored on disk.
type FileRecord struct {
	ID        string      `json:"id"`
	Object    string      `json:"object"`
	Bytes     int64       `json:"bytes"`
	CreatedAt int64       `json:"created_at"`
	Filename  string      `json:"filename"`
	Purpose   FilePurpose `json:"purpose"`
	Status    string      `json:"status"`
}

// RequestCounts mirrors OpenAI batch request_counts.
type RequestCounts struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
}

// Status is the OpenAI batch status string.
type Status string

const (
	StatusValidating Status = "validating"
	StatusInProgress Status = "in_progress"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
	StatusCancelling Status = "cancelling"
	StatusCancelled  Status = "cancelled"
	StatusExpired    Status = "expired"
)

// Meta is persisted batch job state for the OpenAI facade.
type Meta struct {
	ID               string `json:"id"`
	Object           string `json:"object"`
	Endpoint         string `json:"endpoint"`
	InputFileID      string `json:"input_file_id"`
	OutputFileID     string `json:"output_file_id,omitempty"`
	ErrorFileID      string `json:"error_file_id,omitempty"`
	CompletionWindow string `json:"completion_window"`
	Status           Status `json:"status"`
	BackendID        string `json:"backend_id"`
	PublicModel      string `json:"public_model"`
	CFModel          string `json:"cf_model"`
	CFRequestID      string `json:"cf_request_id"`
	// UpstreamBatchID is the provider batch id for non-Cloudflare backends (Claude Message Batches).
	UpstreamBatchID string        `json:"upstream_batch_id,omitempty"`
	RequestCounts   RequestCounts `json:"request_counts"`
	CreatedAt       int64         `json:"created_at"`
	InProgressAt    int64         `json:"in_progress_at,omitempty"`
	CompletedAt     int64         `json:"completed_at,omitempty"`
	FailedAt        int64         `json:"failed_at,omitempty"`
	CancelledAt     int64         `json:"cancelled_at,omitempty"`
	ExpiresAt       int64         `json:"expires_at,omitempty"`
}

// PublicBatch is the OpenAI Batch object returned on the HTTP API.
// Keep OpenAI-facing fields in sync with Meta (excluding adapter-only columns).
type PublicBatch struct {
	ID               string        `json:"id"`
	Object           string        `json:"object"`
	Endpoint         string        `json:"endpoint"`
	InputFileID      string        `json:"input_file_id"`
	OutputFileID     string        `json:"output_file_id,omitempty"`
	ErrorFileID      string        `json:"error_file_id,omitempty"`
	CompletionWindow string        `json:"completion_window"`
	Status           Status        `json:"status"`
	RequestCounts    RequestCounts `json:"request_counts"`
	CreatedAt        int64         `json:"created_at"`
	InProgressAt     int64         `json:"in_progress_at,omitempty"`
	CompletedAt      int64         `json:"completed_at,omitempty"`
	FailedAt         int64         `json:"failed_at,omitempty"`
	CancelledAt      int64         `json:"cancelled_at,omitempty"`
	ExpiresAt        int64         `json:"expires_at,omitempty"`
}

// Public returns the OpenAI-shaped batch resource without adapter-only fields.
func (m Meta) Public() PublicBatch {
	obj := strings.TrimSpace(m.Object)
	if obj == "" {
		obj = "batch"
	}
	return PublicBatch{
		ID:               m.ID,
		Object:           obj,
		Endpoint:         m.Endpoint,
		InputFileID:      m.InputFileID,
		OutputFileID:     m.OutputFileID,
		ErrorFileID:      m.ErrorFileID,
		CompletionWindow: m.CompletionWindow,
		Status:           m.Status,
		RequestCounts:    m.RequestCounts,
		CreatedAt:        m.CreatedAt,
		InProgressAt:     m.InProgressAt,
		CompletedAt:      m.CompletedAt,
		FailedAt:         m.FailedAt,
		CancelledAt:      m.CancelledAt,
		ExpiresAt:        m.ExpiresAt,
	}
}
