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

func Test_isValidTriggerAt(t *testing.T) {
	// valid (2038 or something like that)
	if err := isValidTriggerAt("2174245620"); err != nil {
		t.Error("Expected to succeed (Unix time stamp), failed with", err)
	}

	for _, relativeTime := range []string{"+1d", "+24h", "+1440m"} {
		if err := isValidTriggerAt(relativeTime); err != nil {
			t.Error("Expected to succeed with relative time", relativeTime, "failed with", err)
		}
	}

	// error: bad input
	for _, input := range []string{"", "+", "+m", "+6", "6h", "+6z", "+1k", "112a"} {
		if err := isValidTriggerAt(input); err == nil {
			t.Error("Expected to fail with bad input", input)
		}
	}

	// not in the future
	if err := isValidTriggerAt("1227560820"); err == nil {
		t.Error("Expected to fail (past)")
	}

	// future but not 1-minute resolution

	if err := isValidTriggerAt("2174245625"); err == nil {
		t.Error("Expected to fail (not 1-minute)")
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
		tsk.NormalizeTriggerAt()
		if tsk.TriggerAt != expect {
			t.Error("Expected", expect, "on", relativeTime, "got", tsk.TriggerAt)
		}
	}

	// no-op with a Unix timestamp
	tsk.TriggerAt = expect
	tsk.NormalizeTriggerAt()
	if tsk.TriggerAt != expect {
		t.Error("Expected no-op with", expect, "on", "got", tsk.TriggerAt)
	}
}

func TestTask_NormalizeTag(t *testing.T) {
	tsk := New()

	tsk.Tag = ""
	tsk.NormalizeTag()
	if tsk.Tag[:1] != delimiterTagUUID {
		t.Error("Expected empty tag followed by UUID delimiter, got", tsk.Tag[:1])
	}

	tsk.Tag = "abc"
	tsk.NormalizeTag()
	if tsk.Tag[:4] != "abc"+delimiterTagUUID {
		t.Error("Expected full tag followed by UUID delimiter, got", tsk.Tag[:4])
	}
}
