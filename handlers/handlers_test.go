package handlers

import (
	"strconv"
	"testing"

	"github.com/marcoalmeida/callme/util"
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

func Test_parseTriggerOn(t *testing.T) {
	// valid (2038 or something like that)
	_, err := parseTriggerAt("2174245620")
	if err != nil {
		t.Error("Expected to succeed (Unix time stamp), failed with", err)
	}

	// valid relative time
	currentMinute := util.GetUnixMinute()
	// 1 day from now
	expect := currentMinute + 86400
	for _, relativeTime := range []string{"+1d", "+24h", "+1440m"} {
		at, err := parseTriggerAt(relativeTime)
		if err != nil {
			t.Error("Expected to succeed with relative time", relativeTime, "failed with", err)
		}
		if at != strconv.FormatInt(expect, 10) {
			t.Error("Expected", expect, "got", at)
		}
	}

	// error: bad input
	for _, input := range []string{"", "+", "+m", "+6", "6h", "+6z", "+1k", "112a"} {
		tm, err := parseTriggerAt(input)
		if err == nil {
			t.Error("Expected to fail with bad input", input, ", succeeded returning", tm)
		}
	}

	// not in the future
	tm, err := parseTriggerAt("1227560820")
	if err == nil {
		t.Error("Expected to fail (past), succeeded returning", tm)
	}

	// future but not 1-minute resolution
	tm, err = parseTriggerAt("2174245625")
	if err == nil {
		t.Error("Expected to fail (not 1-minute), succeeded returning", tm)
	}
}
