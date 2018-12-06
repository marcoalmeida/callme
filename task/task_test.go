package task

import (
	"strconv"
	"testing"

	"github.com/marcoalmeida/callme/util"
)

func Test_isValidTag(t *testing.T) {
	// valid tags
	for _, tag := range []string{"", "a", "aB", "a1b", "1"} {
		if err := isValidTag(tag); err != nil {
			t.Error("Expected to succeed, failed on", tag)
		}
	}

	// invalid tags
	for _, tag := range []string{"a-b", "[]", "+as", "a@b", "1-5"} {
		if err := isValidTag(tag); err == nil {
			t.Error("Expected to fail, succeeded on", tag)
		}
	}
}

func TestTask_NormalizeTriggerAt(t *testing.T) {
	tsk := New()

	// valid relative time
	currentMinute := util.GetUnixMinute()
	// 1 day from now
	expect := strconv.FormatInt(currentMinute+86400, 10)
	for _, relativeTime := range []string{"+1d", "+24h", "+1440m"} {
		tsk.TriggerAt = relativeTime
		if err := tsk.NormalizeTriggerAt(); err != nil {
			t.Error(err)
		}
		if tsk.TriggerAt != expect {
			t.Error("Expected", expect, "on", relativeTime, "got", tsk.TriggerAt)
		}
	}

	// no-op with a Unix timestamp
	tsk.TriggerAt = expect
	if err := tsk.NormalizeTriggerAt(); err != nil {
		t.Error(err)
	}
	if tsk.TriggerAt != expect {
		t.Error("Expected no-op with", expect, "on", "got", tsk.TriggerAt)
	}

	// valid (2038 or something like that)
	tsk.TriggerAt = "2174245620"
	if err := tsk.NormalizeTriggerAt(); err != nil {
		t.Error("Expected to succeed (Unix time stamp), failed with", err)
	}

	for _, relativeTime := range []string{"+1d", "+24h", "+1440m"} {
		tsk.TriggerAt = relativeTime
		if err := tsk.NormalizeTriggerAt(); err != nil {
			t.Error("Expected to succeed with relative time", relativeTime, "failed with", err)
		}
	}

	// error: bad input
	for _, input := range []string{"", "+", "+m", "+6", "6h", "+6z", "+1k", "112a"} {
		tsk.TriggerAt = input
		if err := tsk.NormalizeTriggerAt(); err == nil {
			t.Error("Expected to fail with bad input", input)
		}
	}

	// not in the future
	tsk.TriggerAt = "1227560820"
	if err := tsk.NormalizeTriggerAt(); err == nil {
		t.Error("Expected to fail (past)")
	}

	// future but not 1-minute resolution
	tsk.TriggerAt = "2174245625"
	if err := tsk.NormalizeTriggerAt(); err == nil {
		t.Error("Expected to fail (not 1-minute)")
	}
}

func TestTaskID_IsValid(t *testing.T) {
	// valid
	for _, tid := range []string{"+uuid@12345", "tag+uuid@12345"} {
		if !IsValidTaskID(tid) {
			t.Error("Expected", tid, "to be a valid task ID")
		}
	}

	// invalid
	for _, tid := range []string{"", "uuid", "uuid@12345", "tag+uuid@12d345", "tag+uu+id@12345", "@34", "@"} {
		if IsValidTaskID(tid) {
			t.Error("Expected", tid, "to be a invalid task ID")
		}
	}
}
