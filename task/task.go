package task

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/marcoalmeida/callme/util"
	"go.uber.org/zap"
)

const (
	Pending                   = "pending"
	Running                   = "running"
	Successful                = "successful"
	Failed                    = "failed"
	Skipped                   = "skipped"
	defaultCallbackMethod     = "GET"
	defaultRetry              = 1
	defaultExpectedHTTPStatus = 200
	defaultMaxDelay           = 10
	// maximum number of bytes from the response to store
	maxResponseBytes  = 256
	delimiterTagUUID  = "+"
	delimiterUniqueID = "@"
)

// avoid compiling the regular expressions several times per request
var reValidTriggerAt = regexp.MustCompile("[+]([0-9]+)([mhd])")
var reValidTag = regexp.MustCompile("[a-zA-Z0-9]*")

// Task represents a unit of work to be executed at some point in the future
type Task struct {
	TriggerAt          string `json:"trigger_at"`
	Tag                string `json:"tag,omitempty"`
	Payload            string `json:"payload,omitempty"`
	CallbackEndpoint   string `json:"callback"`
	CallbackMethod     string `json:"callback_method,omitempty"`
	Retry              int    `json:"retry,omitempty"`
	ExpectedHTTPStatus int    `json:"expected_http_status,omitempty"`
	MaxDelay           int    `json:"max_delay,omitempty"`
	TaskState          string `json:"task_state"`
	ResponseBody       string `json:"response_body,omitempty"`
	ResponseStatus     int    `json:"response_status,omitempty"`
	ExecutedAt         string `json:"executed_at,omitempty"`
}

func New() Task {
	t := Task{}
	t.setDefaults()
	return t
}

func (t Task) String() string {
	return fmt.Sprintf("%s -> %s", t.UniqueID(), t.CallbackEndpoint)
}

func (t Task) UniqueID() string {
	return fmt.Sprintf("%s%s%s", t.Tag, delimiterUniqueID, t.TriggerAt)
}

// isValidTriggerAt returns nil iff ts is a valid time definition, i.e.,
// a Unix epoch timestamp (with 1-minute resolution) or a relative time specification of the form
// +<int><time_identifier>.
func isValidTriggerAt(ts string) error {
	// future Unix timestamps have way more than 3 characters;
	// a valid format is of the form `+<int><time_identifier>`;
	// neither can be less than 3 chars
	if len(ts) < 3 {
		return errors.New("invalid time specification")
	}

	// current minute
	now := util.GetUnixMinute()

	// are we being given a Unix time stamp or a relative time format?
	// relative time specifications start with +
	relative := ts[:1] == "+"
	if relative {
		parts := reValidTriggerAt.FindStringSubmatch(ts)
		if len(parts) != 3 {
			return errors.New("relative time specification does not match " + reValidTriggerAt.String())

		}
	} else {
		// ts is a Unix time stamp
		inputTime, err := strconv.Atoi(ts)
		if err != nil {
			return errors.New("invalid Unix timestamp")
		}
		// enforce time with 1-minute resolution
		if inputTime%60 != 0 {
			return errors.New("timestamp must be on 1-minute resolution")
		}
		// make sure it's in the future
		if int64(inputTime) <= now {
			return errors.New("timestamp must be in the future")
		}
	}

	return nil
}

// isValidTag returns nil iff tag is valid, i.e., an arbitrary alphanumeric string
func isValidTag(tag string) error {
	if len(reValidTag.FindAllString(tag, -1)) != 1 {
		return errors.New("invalid tag: does not match " + reValidTag.String())
	}

	return nil
}

// NormalizeTriggerAt ensures the value of the .TriggerAt field is a Unix timestamp.
// It should only be called if isValidTriggerAt returns nil as there is no error handling.
func (t *Task) NormalizeTriggerAt() {
	// if trigger_at is already a Unix timestamp there's nothing to do
	if t.TriggerAt[:1] != "+" {
		return
	}
	// current minute
	now := util.GetUnixMinute()

	parts := reValidTriggerAt.FindStringSubmatch(t.TriggerAt)
	spec := parts[2]
	inputTime, _ := strconv.Atoi(parts[1])

	switch spec {
	case "m":
		t.TriggerAt = strconv.FormatInt(now+int64(inputTime)*60, 10)
	case "h":
		t.TriggerAt = strconv.FormatInt(now+int64(inputTime)*3600, 10)
	case "d":
		t.TriggerAt = strconv.FormatInt(now+int64(inputTime)*60*86400, 10)
	}
}

// NormalizeTag ensures uniqueness of the pair (trigger_at,
// tag) by appending a UUID to the value originally set by the user.
func (t *Task) NormalizeTag() {
	u := uuid.New()
	// a normalized tag is of the form tag+UUID
	// remove the - characters from the UUID
	t.Tag = fmt.Sprintf("%s%s%s", t.Tag, delimiterTagUUID, strings.Replace(u.String(), "-", "", -1))
}

func ParseUniqueID(id string) (Task, error) {
	parts := strings.Split(id, delimiterUniqueID)
	if len(parts) != 2 {
		return Task{}, errors.New("expected exactly 2 components on the unique ID")
	}

	return Task{TriggerAt: parts[1], Tag: parts[0]}, nil
}

// IsValid returns nil iff all fields in the task t are valid.
func (t Task) IsValid() error {
	// make sure the timestamp is valid
	if err := isValidTriggerAt(t.TriggerAt); err != nil {
		return err
	}
	// make sure the tag is valid
	if err := isValidTag(t.Tag); err != nil {
		return err
	}
	// validate the callback endpoint
	_, err := url.ParseRequestURI(t.CallbackEndpoint)
	if err != nil {
		return err
	}

	// retry and max delay are optional but if present cannot be negative
	if t.Retry < 0 {
		return errors.New("retry must be a non-negative integer")
	}

	if t.MaxDelay < 0 {
		return errors.New("max_delay must be a non-negative integer")
	}

	// the client may optionally provide a specific HTTP method, in which case it needs to be one of GET, POST, PUT,
	// DELETE
	if !(t.CallbackMethod == "GET" ||
		t.CallbackMethod == "POST" ||
		t.CallbackMethod == "PUT" ||
		t.CallbackMethod == "DELETE") {
		return errors.New("unsupported HTTP method:" + t.CallbackMethod)
	}

	return nil
}

// setDefaults updates the task to make sure all optional values are set
func (t *Task) setDefaults() {
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

// DoCallback hits the callback endpoint, with the provided payload,
// using the specified HTTP method. On failure it will retry, using exponential backoff logic,
// up until the number of times set. Finally, it will update the Status and ResponseBody fields.
func (t Task) DoCallback(httpClient *http.Client, updateTask func(Task) (string, error), logger *zap.Logger) {
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
	_, err := updateTask(t)
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

	logger.Debug("DoCallback completed", zap.String("task", t.String()), zap.Int("http_status", status))

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
	_, err = updateTask(t)
	if err != nil {
		logger.Error("Failed to update task", zap.Error(err), zap.String("task", t.String()))
	}

	logger.Debug("Task updated", zap.String("task", t.String()), zap.Int("http_status", status))
}
