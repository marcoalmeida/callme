package handlers

// Message provides a simple way of defining a response message that can easily be attached to ResponseBody
type message struct {
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

type createTaskResponse struct {
	TaskID string `json:"task_id"`
}
