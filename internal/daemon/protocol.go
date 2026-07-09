package daemon

const (
	RequestQuery        = "query"
	RequestModelSwitch  = "model_switch"
	RequestModelCurrent = "model_current"
	RequestModelList    = "model_list"

	ResponseChunk       = "chunk"
	ResponseDone        = "done"
	ResponseError       = "error"
	ResponseModel       = "model"
	ResponseModelList   = "model_list"
	ResponseModelSwitch = "model_switch"
)

type Request struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Prompt    string `json:"prompt,omitempty"`
	Model     string `json:"model,omitempty"`
	Reset     bool   `json:"reset,omitempty"`
}

type Response struct {
	Type         string         `json:"type"`
	Content      string         `json:"content,omitempty"`
	Error        string         `json:"error,omitempty"`
	CurrentModel string         `json:"current_model,omitempty"`
	DefaultModel string         `json:"default_model,omitempty"`
	Profiles     []ModelProfile `json:"profiles,omitempty"`
}

type ModelProfile struct {
	Profile   string `json:"profile"`
	Model     string `json:"model"`
	Installed bool   `json:"installed"`
}
