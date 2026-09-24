package imageactivity

import "encoding/json"

type UpstreamReceipt struct {
	Revision string `json:"revision"`
}
type ModelReceipt struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}
type ResumeResult struct {
	Control Control `json:"control"`
}

type Control struct {
	ID             string `json:"id"`
	Paused         bool   `json:"paused"`
	Reason         string `json:"reason"`
	Revision       string `json:"revision"`
	UncertainSlots int    `json:"uncertain_slots"`
}
type ControlRow struct {
	Control
	Current bool `json:"current"`
	Queued  int  `json:"queued"`
	Running int  `json:"running"`
}
type Upstream struct {
	Revision                string   `json:"revision"`
	Configured              bool     `json:"configured"`
	BaseURL                 string   `json:"base_url"`
	SecretSet               bool     `json:"secret_set"`
	RPM                     *int     `json:"rpm"`
	Concurrency             *int     `json:"concurrency"`
	PerUserLimit            int      `json:"per_user_limit"`
	GlobalLimit             int      `json:"global_limit"`
	QueueTimeoutSeconds     int      `json:"queue_timeout_seconds"`
	ExecutionTimeoutSeconds int      `json:"execution_timeout_seconds"`
	MemoryBudgetMiB         int      `json:"memory_budget_mib"`
	ImageOrigins            []string `json:"image_origins"`
	Adapter                 *Adapter `json:"adapter"`
	Control                 *Control `json:"control"`
}
type SecretInput struct {
	Mode  string  `json:"mode"`
	Value *string `json:"value,omitempty"`
}
type UpstreamInput struct {
	ExpectedRevision        string      `json:"expected_revision"`
	BaseURL                 string      `json:"base_url"`
	Secret                  SecretInput `json:"secret"`
	RPM                     int         `json:"rpm"`
	Concurrency             int         `json:"concurrency"`
	PerUserLimit            int         `json:"per_user_limit"`
	GlobalLimit             int         `json:"global_limit"`
	QueueTimeoutSeconds     int         `json:"queue_timeout_seconds"`
	ExecutionTimeoutSeconds int         `json:"execution_timeout_seconds"`
	MemoryBudgetMiB         int         `json:"memory_budget_mib"`
	ImageOrigins            []string    `json:"image_origins"`
	Adapter                 Adapter     `json:"adapter"`
}
type Constant struct {
	Pointer string          `json:"pointer"`
	Value   json.RawMessage `json:"value"`
}
type Mapping struct {
	ModelPointer string                  `json:"model_pointer"`
	Parameters   map[ParameterKey]string `json:"parameters"`
	Constants    []Constant              `json:"constants"`
}
type DiscoveryAdapter struct {
	Method          string  `json:"method"`
	Path            string  `json:"path"`
	ItemsPointer    string  `json:"items_pointer"`
	IDPointer       string  `json:"id_pointer"`
	MetadataPointer *string `json:"metadata_pointer,omitempty"`
}
type SubmitAdapter struct {
	Method  string  `json:"method"`
	Path    string  `json:"path"`
	Mapping Mapping `json:"mapping"`
}
type PollAdapter struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}
type ResponseAdapter struct {
	TaskIDPointer *string  `json:"task_id_pointer,omitempty"`
	StatePointer  *string  `json:"state_pointer,omitempty"`
	WorkingStates []string `json:"working_states"`
	SuccessStates []string `json:"success_states"`
	FailureStates []string `json:"failure_states"`
	ImagesPointer string   `json:"images_pointer"`
	Base64Pointer *string  `json:"base64_pointer,omitempty"`
	URLPointer    *string  `json:"url_pointer,omitempty"`
}
type Adapter struct {
	Discovery DiscoveryAdapter `json:"discovery"`
	Submit    SubmitAdapter    `json:"submit"`
	Poll      *PollAdapter     `json:"poll,omitempty"`
	Response  ResponseAdapter  `json:"response"`
}
type AdminModel struct {
	ID              string            `json:"id"`
	UpstreamModelID string            `json:"upstream_model_id"`
	Metadata        json.RawMessage   `json:"metadata"`
	Configured      bool              `json:"configured"`
	Revision        string            `json:"revision"`
	DisplayName     string            `json:"display_name"`
	Description     string            `json:"description"`
	Enabled         bool              `json:"enabled"`
	Price           Price             `json:"price"`
	Parameters      []ParameterRule   `json:"parameters"`
	Combinations    []CombinationRule `json:"combinations"`
	Mapping         Mapping           `json:"mapping"`
}
type ModelInput struct {
	ExpectedRevision string            `json:"expected_revision"`
	DisplayName      string            `json:"display_name"`
	Description      string            `json:"description"`
	Enabled          bool              `json:"enabled"`
	Price            Price             `json:"price"`
	Parameters       []ParameterRule   `json:"parameters"`
	Combinations     []CombinationRule `json:"combinations"`
	Mapping          Mapping           `json:"mapping"`
}
type Refresh struct {
	ID          string  `json:"id"`
	State       string  `json:"state"`
	CreatedAt   int64   `json:"created_at"`
	CompletedAt *int64  `json:"completed_at"`
	ModelCount  int     `json:"model_count"`
	ErrorCode   *string `json:"error_code"`
}
type RefreshResult struct {
	Operation Refresh `json:"operation"`
}
type ResumeInput struct {
	ControlID        string `json:"control_id"`
	ExpectedRevision string `json:"expected_revision"`
	Reason           string `json:"reason"`
}
