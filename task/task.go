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
	"github.com/marcoalmeida/callme/types"
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

// Task represents a unit of work to be executed at some point in the future.
// It should directly map to a row in DynamoDB
type Task struct {
	TriggerAt          string `json:"trigger_at"` // hash key
	UUID               string `json:"uuid"`
	Tag                string `json:"tag,omitempty"`
	TagUUID            string `json:"tag_uuid"` // range key
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

// TaskID is the type for the unique identifier of a Task instance
type TaskID string

func (tid TaskID) String() string {
	return string(tid)
}

// New returns a new Task instance with default values set
func New() Task {
	t := Task{}

	// make sure all optional fields are set
	t.setDefaults()

	// generate a UUID for this task
	u := uuid.New()
	// remove the - characters from the UUID
	t.UUID = strings.Replace(u.String(), "-", "", -1)

	return t
}

// NewFromCreateRequest returns a new Task instance with all fields initialized with the values set on tr.
func NewFromCreateRequest(tr types.CreateTaskRequest) Task {
	t := New()

	t.TriggerAt = tr.TriggerAt
	t.Tag = tr.Tag
	t.Payload = tr.Payload
	t.CallbackEndpoint = tr.CallbackEndpoint
	t.CallbackMethod = tr.CallbackMethod
	t.Retry = tr.Retry
	t.ExpectedHTTPStatus = tr.ExpectedHTTPStatus
	t.MaxDelay = tr.MaxDelay

	t.setDefaults()

	return t
}

func (t Task) String() string {
	return fmt.Sprintf("%s -> %s", t.UniqueID(), t.CallbackEndpoint)
}

// PrepareForDynamoDB updates all the necessary fields so that the Task instance is ready for being
// written to DynamoDB
func (t *Task) PrepareForDynamoDB() {
	// right now only the range key needs to be set, as we're using .TriggerAt as the hash key
	t.TagUUID = fmt.Sprintf("%s%s%s", t.Tag, delimiterTagUUID, t.UUID)
}

// UniqueID returns a string that uniquely identifies a task
func (t Task) UniqueID() TaskID {
	return TaskID(fmt.Sprintf("%s%s%s", t.TagUUID, delimiterUniqueID, t.TriggerAt))
}

func ParseUniqueID(id string) (Task, error) {
	parts := strings.Split(id, delimiterUniqueID)
	if len(parts) != 2 {
		return Task{}, errors.New("expected exactly 2 components on the unique ID")
	}

	return Task{TriggerAt: parts[1], Tag: parts[0]}, nil
}

// NormalizeTriggerAt makes sure the .TriggerAt field is a valid Unix timestamp with 1-minute resolution.
// The input is validated and if a relative time specification the exact target time is calculated
// and the field updated.
// An error is returned it if the input is not valid.
func (t *Task) NormalizeTriggerAt() error {
	// Unix timestamps (now and in the future) have way more than 3 characters;
	// a valid format is of the form `+<int><time_identifier>`;
	// neither form can be less than 3 chars
	if len(t.TriggerAt) < 3 {
		return errors.New("invalid time specification")
	}

	// current minute
	now := util.GetUnixMinute()

	// are we being given a Unix time stamp or a relative time format?
	// relative time specifications start with +
	relative := t.TriggerAt[:1] == "+"
	if relative {
		parts := reValidTriggerAt.FindStringSubmatch(t.TriggerAt)
		if len(parts) != 3 {
			return errors.New("relative time specification does not match " + reValidTriggerAt.String())
		}

		spec := parts[2]
		inputTime, err := strconv.Atoi(parts[1])
		if err != nil {
			return err
		}
		switch spec {
		case "m":
			t.TriggerAt = strconv.FormatInt(now+int64(inputTime)*60, 10)
		case "h":
			t.TriggerAt = strconv.FormatInt(now+int64(inputTime)*3600, 10)
		case "d":
			t.TriggerAt = strconv.FormatInt(now+int64(inputTime)*86400, 10)
		default:
			return errors.New("invalid relative time specifier")
		}
	} else {
		// triggerAt is a Unix time stamp --> check for errors
		inputTime, err := strconv.Atoi(t.TriggerAt)
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

	// all good
	return nil
}

// validateTag returns nil iff tag is an alphanumeric string
func isValidTag(tag string) error {
	if len(reValidTag.FindAllString(tag, -1)) != 1 {
		return errors.New("invalid tag: does not match " + reValidTag.String())
	}

	return nil
}

// ValidateAndNormalize returns nil iff all fields in the task are valid.
// It also normalizes the .TriggerAt field converting it, if necessary, from a relative time
// specification into a Unix timestamp.
func (t Task) ValidateAndNormalize() error {
	// make sure the timestamp is valid
	if err := t.NormalizeTriggerAt(); err != nil {
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
