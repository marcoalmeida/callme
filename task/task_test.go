package task

import "testing"

func TestIsValidTag(t *testing.T) {
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

}
