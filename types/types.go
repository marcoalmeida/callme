package types

// BasicResponse provides a simple way of defining a response message that can easily be attached to ResponseBody
type BasicResponse struct {
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

type CreateTaskRequest struct {
	TriggerAt          string `json:"trigger_at"`
	Tag                string `json:"tag,omitempty"`
	Payload            string `json:"payload,omitempty"`
	CallbackEndpoint   string `json:"callback"`
	CallbackMethod     string `json:"callback_method,omitempty"`
	Retry              int    `json:"retry,omitempty"`
	ExpectedHTTPStatus int    `json:"expected_http_status,omitempty"`
	MaxDelay           int    `json:"max_delay,omitempty"`
}

type CreateTaskResponse struct {
	TaskID string `json:"task_id"`
}
