package handlers

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/marcoalmeida/callme/app"
	"github.com/marcoalmeida/callme/task"
	"github.com/marcoalmeida/callme/types"
	"github.com/marcoalmeida/callme/util"
	"go.uber.org/zap"
)

// Response contains the necessary data to send an HTTP response back to the client. It should
// be an interface that needs to be JSON-serialized before sending.
type Response struct {
	status int
	data   interface{}
}

// Handler is used to set up all of the handlers in the basic environment on which we're service traffic
type Handler struct {
	App         *app.CallMe
	handlerFunc func(e *app.CallMe, r *http.Request) *Response
}

// Register registers all handlers
func Register(app *app.CallMe) {
	http.Handle("/task/", Handler{App: app, handlerFunc: taskHandler})
	http.Handle("/reschedule/", Handler{App: app, handlerFunc: rescheduleHandler})
	http.Handle("/status/", Handler{App: app, handlerFunc: statusHandler})
}

// ServeHTTP implements http.Handler and sends the actual response back to the client.
func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var err error
	pretty := false

	// we only care about ParseForm (which is idempotent, and safe to call even
	// if already called by a handler) to get the pretty parameter which can be used
	// by any endpoint
	err = r.ParseForm()
	if err == nil {
		_, pretty = r.Form["pretty"]
	}

	// run the handler and get the response to be sent to the client
	resp := h.handlerFunc(h.App, r)
	// start by sending the HTTP status code
	w.WriteHeader(resp.status)
	// (try to) parse the JSON data and send the response
	enc := json.NewEncoder(w)
	if pretty {
		enc.SetIndent("", "    ")
	}
	err = enc.Encode(resp.data)

	// all we can do is log the error
	if err != nil {
		h.App.Logger.Error("Failed to send response", zap.Error(err))
	}
}

// auxiliary function to respond with an internal server error
func internalServerError(msg string) *Response {
	return &Response{
		status: http.StatusInternalServerError,
		data:   types.BasicResponse{Error: msg},
	}
}

func badRequestError(msg string) *Response {
	return &Response{
		status: http.StatusBadRequest,
		data:   types.BasicResponse{Error: msg},
	}
}

func unknownMethodError() *Response {
	return &Response{
		status: http.StatusBadRequest,
		data:   types.BasicResponse{Error: "unknown method"},
	}
}

func taskHandler(callme *app.CallMe, r *http.Request) *Response {
	err := r.ParseForm()
	if err != nil {
		return internalServerError(err.Error())
	}

	defer r.Body.Close()
	payload, err := ioutil.ReadAll(r.Body)
	if err != nil {
		callme.Logger.Error("Failed to read request body", zap.Error(err))
		return internalServerError("failed to read the request body")
	}

	switch r.Method {
	case "GET":
		// TODO:
		return &Response{
			status: http.StatusNotImplemented,
			data:   types.BasicResponse{Error: "not yet implemented"},
		}
	case "PUT":
		tr := types.CreateTaskRequest{}
		// try to parse the payload and build a task request instance
		err := json.Unmarshal(payload, &tr)
		if err != nil {
			// this error is safe (and useful) to return to the client
			return badRequestError(err.Error())
		}

		// create a Task instance from the request
		t := task.NewFromCreateRequest(tr)
		// validate the input and make sure to convert a relative time specification into a Unix timestamp
		if err := t.ValidateAndNormalize(); err != nil {
			return badRequestError(err.Error())
		}

		// try to create the task
		id, err := callme.CreateTask(t)
		if err != nil {
			callme.Logger.Error("Failed to create task", zap.Error(err))
			return internalServerError(err.Error())
		}

		// we successfully created the task, return the unique ID
		return &Response{
			status: http.StatusOK,
			data:   types.CreateTaskResponse{TaskID: taskID},
		}
	case "POST":
		// TODO: (update)
		return &Response{
			status: http.StatusNotImplemented,
			data:   types.BasicResponse{Error: "not yet implemented"},
		}
	case "DELETE":
		// TODO:
		return &Response{
			status: http.StatusNotImplemented,
			data:   types.BasicResponse{Error: "not yet implemented"},
		}
	default:
		return unknownMethodError()
	}
}

// move a failed task back to the queue
// - status of a specific task:             /reschedule/<task_name>@<trigger_at>
// - status of all tasks with a given name: /reschedule/<task_name>
// defaults to rescheduling only failed tasks, use ?all=true to override
func rescheduleHandler(callme *app.CallMe, r *http.Request) *Response {
	// POST is the only method this endpoint handles
	if r.Method != "POST" {
		return unknownMethodError()
	}

	err := r.ParseForm()
	if err != nil {
		return internalServerError(err.Error())
	}

	taskParam := r.URL.Path[len("/reschedule/"):]

	// create a task instance, or part of it if the trigger timestamp is missing, out of the URL path
	taskName, triggerAt := parseTaskIdentifier(taskParam)
	tsk := task.Task{
		Tag:       taskName,
		TriggerAt: triggerAt,
	}

	// get the new time on which the task is supposed to be retried
	inputTriggerAt := r.Form.Get("trigger_at")
	if inputTriggerAt == "" {
		// default to running it now, with a little slack just in case the current minute is already being processed
		inputTriggerAt = strconv.FormatInt(util.GetUnixMinute()+60, 10)
	} else {
		inputTriggerAt, err = parseTriggerAt(inputTriggerAt)
		if err != nil {
			return &Response{
				status: http.StatusBadRequest,
				data:   types.BasicResponse{Error: err.Error()},
			}
		}
	}

	// process just the failed entries or all?
	_, all := r.Form["all"]

	callme.Logger.Debug(
		"Processing request for /reschedule/",
		zap.String("task", tsk.String()),
		zap.String("trigger_at", tsk.TriggerAt),
		zap.Bool("all", all),
	)
	newTasks, err := callme.Reschedule(tsk, inputTriggerAt, all)
	if err != nil {
		return &Response{
			status: http.StatusInternalServerError,
			data:   types.BasicResponse{Error: err.Error()},
		}
	}

	// respond with the updated task
	return &Response{
		status: http.StatusOK,
		data:   newTasks,
	}
}

// callme's global status:
// - status of a specific task:             /status/<task_name>@<trigger_at>
// - status of all tasks with a given name: /status/<task_name>[?start_from=<task_name>@<trigger_at>&future_only=true]
// - status of all tasks:                   /status/?start_from=<task_name>@<trigger_at>[?future_only=true]
func statusHandler(callme *app.CallMe, r *http.Request) *Response {
	// GET is the only method this endpoint handles
	if r.Method != "GET" {
		return unknownMethodError()
	}

	err := r.ParseForm()
	if err != nil {
		return internalServerError(err.Error())
	}

	taskParam := r.URL.Path[len("/status/"):]

	// create a task instance, or part of it if the trigger timestamp is missing, out of the URL path
	taskName, triggerAt := parseTaskIdentifier(taskParam)
	tsk := task.Task{
		Tag:       taskName,
		TriggerAt: triggerAt,
	}
	// create a task instance from the start_from parameter, necessary for pagination
	taskName, triggerAt = parseTaskIdentifier(r.Form.Get("start_from"))
	startFrom := task.Task{
		Tag:       taskName,
		TriggerAt: triggerAt,
	}
	// in case the caller just wants us to list tasks scheduled at some point in the future
	_, futureOnly := r.Form["future_only"]

	callme.Logger.Debug(
		"Processing request for /status/",
		zap.String("task", tsk.String()),
		zap.Bool("future_only", futureOnly),
		zap.String("start_from", startFrom.String()),
	)
	status, err := callme.Status(tsk, startFrom, futureOnly)
	if err != nil {
		return internalServerError(err.Error())
	}

	return &Response{
		status: http.StatusOK,
		data:   status,
	}
}

// TODO: deprecate
// given a task key of the form task_name@trigger_at, where trigger_at is optional,
// parse it and return the individual components
func parseTaskIdentifier(taskKey string) (string, string) {
	taskKeyParts := strings.Split(taskKey, "@")
	taskName := taskKeyParts[0]
	triggerAt := ""
	// trigger_at is optional
	if len(taskKeyParts) == 2 {
		triggerAt = taskKeyParts[1]
	}

	return taskName, triggerAt
}

// if input is a relative time specification, return the corresponding Unix timestamp with 1-minute resolution
// if the input provided is already a unix timestamp, ensure it uses 1-minute resolution
func parseTriggerAt(input string) (string, error) {
	// Unix timestamps (maybe not 30 years ago, but now) have way more than 3 characters
	// a valid format is of the form `+<int><time_identifier>` which cannot be less than 3 chars
	if len(input) < 3 {
		return "", errors.New("invalid format for trigger_at: " + input)
	}
	// current minute
	now := util.GetUnixMinute()

	// are we being given a Unix time stamp or a relative time format?
	// relative time specifications start with +
	relative := input[:1] == "+"
	if relative {
		// validate the input
		// re := regexp.MustCompile(task.ValidTriggerAtRE)
		re := regexp.MustCompile("")
		parts := re.FindStringSubmatch(input)
		if len(parts) != 3 {
			return "", errors.New("invalid relative time specification")
		}
		// extract the relative time and compute the corresponding Unix time stamp
		spec := parts[2]
		// if the regexp matches Atoi should never fail, still
		inputTime, err := strconv.Atoi(parts[1])
		if err != nil {
			return "", errors.New("invalid integer in relative time specification")
		}
		// convert whatever time value we received to seconds and add to the current time stamp
		switch spec {
		case "m":
			return strconv.FormatInt(now+int64(inputTime)*60, 10), nil
		case "h":
			return strconv.FormatInt(now+int64(inputTime)*3600, 10), nil
		case "d":
			return strconv.FormatInt(now+int64(inputTime)*86400, 10), nil
		default:
			// we should never reach this because either the regexp matches and the string is successfully parsed,
			// or it does not and we exit before this block
			return "", errors.New("unknown relative time specifier")
		}
	} else {
		// input is a Unix time stamp --> validate it
		inputTime, err := strconv.Atoi(input)
		if err != nil {
			return "", errors.New("invalid Unix time stamp: " + input)
		}
		// enforce time with 1-minute resolution
		if inputTime%60 != 0 {
			return "", errors.New("trigger_at must be on 1-minute resolution")
		}
		// make sure it's in the future
		if int64(inputTime) <= now {
			return "", errors.New("trigger_at must be in the future")
		}
		// all good
		return input, nil
	}
}
