package handlers

import (
	"testing"
)

func Test_parseTaskKey(t *testing.T) {
	taskName, triggerOn := parseTaskIdentifier("")
	if taskName != "" || triggerOn != "" {
		t.Error("Expected empty task name and trigger timestamp, got", taskName, "and", triggerOn)
	}

	for _, tsk := range []string{"t0", "t0@"} {
		taskName, triggerOn := parseTaskIdentifier(tsk)
		if taskName != "t0" || triggerOn != "" {
			t.Error("Expected", tsk, "for task name and trigger timestamp, got", taskName, "and", triggerOn)
		}
	}

	taskName, triggerOn = parseTaskIdentifier("t0@t0")
	if taskName != "t0" || triggerOn != "t0" {
		t.Error("Expected t0 for task name and trigger timestamp, got", taskName, "and", triggerOn)
	}
}
