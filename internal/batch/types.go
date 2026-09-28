package batch

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

// BatchStatus is the OpenAI batch status string.
type BatchStatus string

const (
	BatchStatusValidating BatchStatus = "validating"
	BatchStatusInProgress BatchStatus = "in_progress"
	BatchStatusCompleted  BatchStatus = "completed"
	BatchStatusFailed     BatchStatus = "failed"
	BatchStatusCancelled  BatchStatus = "cancelled"
	BatchStatusExpired    BatchStatus = "expired"
)

// BatchMeta is persisted batch job state for the OpenAI facade.
type BatchMeta struct {
	ID               string        `json:"id"`
	Object           string        `json:"object"`
	Endpoint         string        `json:"endpoint"`
	InputFileID      string        `json:"input_file_id"`
	OutputFileID     string        `json:"output_file_id,omitempty"`
	ErrorFileID      string        `json:"error_file_id,omitempty"`
	CompletionWindow string        `json:"completion_window"`
	Status           BatchStatus   `json:"status"`
	BackendID        string        `json:"backend_id"`
	PublicModel      string        `json:"public_model"`
	CFModel          string        `json:"cf_model"`
	CFRequestID      string        `json:"cf_request_id"`
	RequestCounts    RequestCounts `json:"request_counts"`
	CreatedAt        int64         `json:"created_at"`
	InProgressAt     int64         `json:"in_progress_at,omitempty"`
	CompletedAt      int64         `json:"completed_at,omitempty"`
	FailedAt         int64         `json:"failed_at,omitempty"`
	CancelledAt      int64         `json:"cancelled_at,omitempty"`
	ExpiresAt        int64         `json:"expires_at,omitempty"`
}
