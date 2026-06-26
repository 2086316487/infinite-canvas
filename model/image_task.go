package model

type ImageTaskMode string

const (
	ImageTaskModeGeneration ImageTaskMode = "generation"
	ImageTaskModeEdit       ImageTaskMode = "edit"
)

type ImageTaskStatus string

const (
	ImageTaskStatusPending   ImageTaskStatus = "pending"
	ImageTaskStatusRunning   ImageTaskStatus = "running"
	ImageTaskStatusSucceeded ImageTaskStatus = "succeeded"
	ImageTaskStatusFailed    ImageTaskStatus = "failed"
	ImageTaskStatusCancelled ImageTaskStatus = "cancelled"
)

type ImageTaskOutput struct {
	URL           string `json:"url,omitempty"`
	DataURL       string `json:"dataUrl,omitempty"`
	MimeType      string `json:"mimeType,omitempty"`
	RevisedPrompt string `json:"revisedPrompt,omitempty"`
}

type ImageTask struct {
	ID             string          `json:"id" gorm:"primaryKey"`
	UserID         string          `json:"-" gorm:"index"`
	Mode           ImageTaskMode   `json:"mode"`
	Model          string          `json:"model"`
	Count          int             `json:"count"`
	Status         ImageTaskStatus `json:"status" gorm:"index"`
	Credits        int             `json:"credits"`
	ErrorMessage   string          `json:"error,omitempty"`
	Refunded       bool            `json:"refunded,omitempty"`
	ContentType    string          `json:"-" gorm:"type:text"`
	RequestBody    []byte          `json:"-"`
	UpstreamPath   string            `json:"-" gorm:"type:text"`
	Outputs        []ImageTaskOutput `json:"outputs" gorm:"serializer:json"`
	LockedBy       string            `json:"-" gorm:"index"`
	LockedUntil    string            `json:"-" gorm:"index"`
	Attempts       int               `json:"attempts,omitempty"`
	MaxAttempts    int               `json:"maxAttempts,omitempty" gorm:"-"`
	NextRunAt      string            `json:"-" gorm:"index"`
	StartedAt      string            `json:"startedAt,omitempty"`
	FinishedAt     string            `json:"finishedAt,omitempty"`
	CreatedAt      string            `json:"createdAt"`
	UpdatedAt      string            `json:"updatedAt"`
}
