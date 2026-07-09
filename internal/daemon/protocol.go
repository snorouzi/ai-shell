package daemon

import shellcontext "ai-shell/internal/context"

const (
	RequestQuery         = "query"
	RequestModelSwitch   = "model_switch"
	RequestModelCurrent  = "model_current"
	RequestModelList     = "model_list"
	RequestExecuteConfirm = "execute_confirm"

	ResponseChunk       = "chunk"
	ResponseDone        = "done"
	ResponseError       = "error"
	ResponseModel       = "model"
	ResponseModelList   = "model_list"
	ResponseModelSwitch = "model_switch"
	ResponseSuggestion  = "suggestion"
)

type Request struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Prompt    string `json:"prompt,omitempty"`
	Model     string `json:"model,omitempty"`
	Reset     bool   `json:"reset,omitempty"`
	Approve   bool   `json:"approve,omitempty"`
	Context   shellcontext.Payload `json:"context,omitempty"`
}

type Response struct {
	Type         string         `json:"type"`
	Content      string         `json:"content,omitempty"`
	Error        string         `json:"error,omitempty"`
	CurrentModel string         `json:"current_model,omitempty"`
	DefaultModel string         `json:"default_model,omitempty"`
	Profiles     []ModelProfile `json:"profiles,omitempty"`
	Explanation  string         `json:"explanation,omitempty"`
	Commands     []string       `json:"commands,omitempty"`
	Risk         string         `json:"risk,omitempty"`
}

type ModelProfile struct {
	Profile   string `json:"profile"`
	Model     string `json:"model"`
	Installed bool   `json:"installed"`
}
