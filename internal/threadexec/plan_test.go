package threadexec

import (
	"path/filepath"
	"testing"
)

func TestApprovedPlanRejectsInvalidLocalInputs(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ markdown, attachment string }{
		{"\n\t", ""},
		{string([]byte{0xff}), ""},
		{"plan", "relative.md"},
		{"plan", string(filepath.Separator) + "nul\x00path"},
	} {
		if err := ValidateApprovedPlan(test.markdown, test.attachment); err == nil {
			t.Errorf("ValidateApprovedPlan(%q, %q) succeeded", test.markdown, test.attachment)
		}
	}
}
