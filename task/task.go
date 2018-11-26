package task

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/marcoalmeida/callme/util"
	"go.uber.org/zap"
)

const (
	Pending                    = "pending"
	Running                    = "running"
	Successful                 = "successful"
	Failed                     = "failed"
	Skipped                    = "skipped"
	defaultCallbackMethod            = "GET"
	defaultRetry                     = 1
	defaultExpectedHTTPStatus        = 200
	defaultMaxDelay                  = 10
	// maximum number of bytes from the response to store
	maxResponseBytes = 256
)

type Task struct {
	TriggerAt          string `json:"trigger_at"`
	Name               string `json:"task_name"`
	Payload            string `json:"payload,omitempty"`
	CallbackEndpoint   string `json:"callback"`
	CallbackMethod     string `json:"callback_method,omitempty"`
	Retry              int    `json:"retry,omitempty"`
	ExpectedHTTPStatus int    `json:"expected_http_status,omitempty"`
	MaxDelay           int    `json:"max_delay,omitempty"`
	TaskState          string `json:"task_state"`
	ResponseBody       string `json:"response_body"`
	ResponseStatus     int    `json:"response_status"`
	ExecutedAt         string `json:"executed_at"`
}

func (t Task) String() string {
	return fmt.Sprintf("%s@%s -> %s", t.Name, t.TriggerAt, t.CallbackEndpoint)
}

func (t Task) IsValid() error {
	if t.TriggerAt == "" || t.Name == "" || t.CallbackEndpoint == "" {
		return errors.New("incomplete task definition, required fields missing: trigger_at, task_name, callback")
	}

	if !(t.CallbackMethod == "" ||
		t.CallbackMethod == "GET" ||
		t.CallbackMethod == "POST" ||
		t.CallbackMethod == "PUT" ||
		t.CallbackMethod == "DELETE") {
		return errors.New("unsupported HTTP method:" + t.CallbackMethod)
	}

	return nil
}

func (t *Task) SetDefaults() {
	// initial status
	t.TaskState = Pending

	// HTTP method
	if t.CallbackMethod == "" {
		t.CallbackMethod = defaultCallbackMethod
	}

	// set the number of retries to 1, if it has not been defined
	if t.Retry == 0 {
		t.Retry = defaultRetry
	}

	// default the expected response HTTP status to 200
	if t.ExpectedHTTPStatus == 0 {
		t.ExpectedHTTPStatus = defaultExpectedHTTPStatus
	}

	// default max delay (minutes)
	if t.MaxDelay == 0 {
		t.MaxDelay = defaultMaxDelay
	}
}

// Callback hits the callback endpoint, with the provided payload,
// using the specified HTTP method. On failure it will retry, using exponential backoff logic,
// up until the number of times set. Finally, it will update the Status and ResponseBody fields.
func (t Task) Callback(httpClient *http.Client, updateTask func(Task) error, logger *zap.Logger) {
	var status int
	var response []byte

	logger.Debug("Starting callback", zap.String("task", t.String()))

	// make sure we're not past max delay
	currentMinute := util.GetUnixMinute()
	// by now trigger_at has been validated, it should be safe to ignore the error
	triggerAt, _ := strconv.Atoi(t.TriggerAt)
	if currentMinute > int64(triggerAt)+int64(t.MaxDelay)*60 {
		logger.Error(
			"Skipping callback because we're past max_delay",
			zap.String("trigger_at", t.TriggerAt),
			zap.Int64("current_minute", currentMinute),
			zap.Int("max_delay", t.MaxDelay),
		)
		return
	}

	// update the state before starting
	t.TaskState = Running
	err := updateTask(t)
	if err != nil {
		logger.Error("Failed to update task", zap.Error(err))
	}

	status, response = util.SendHTTPRequest(
		t.CallbackEndpoint,
		[]byte(t.Payload),
		http.Header{},
		t.CallbackMethod,
		httpClient,
		t.ExpectedHTTPStatus,
		t.Retry,
		logger,
	)

	logger.Debug("Callback completed", zap.String("task", t.String()), zap.Int("http_status", status))

	// update the task state
	if status == t.ExpectedHTTPStatus {
		t.TaskState = Successful
	} else {
		t.TaskState = Failed
	}
	// and execution timestamp
	t.ExecutedAt = strconv.FormatInt(time.Now().Unix(), 10)
	// and received HTTP response
	t.ResponseStatus = status
	if len(response) < maxResponseBytes {
		t.ResponseBody = string(response)
	} else {
		t.ResponseBody = string(response[:maxResponseBytes])
	}

	// update the task's state now that we're done
	err = updateTask(t)
	if err != nil {
		logger.Error("Failed to update task", zap.Error(err), zap.String("task", t.String()))
	}

	logger.Debug("Task updated", zap.String("task", t.String()), zap.Int("http_status", status))
}
