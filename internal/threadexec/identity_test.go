package threadexec

import (
	"testing"
)

const canonicalTestThreadID = "67e55044-10b1-426f-9247-bb680e5fe0c8"

func TestThreadExecutionID(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		canonicalTestThreadID,
		"67E5504410B1426F9247BB680E5FE0C8",
		"67E55044-10B1-426F-9247-BB680E5FE0C8",
		"{67E55044-10B1-426F-9247-BB680E5FE0C8}",
		"urn:uuid:67E55044-10B1-426F-9247-BB680E5FE0C8",
	} {
		if got := CanonicalID(input); got != canonicalTestThreadID {
			t.Errorf("normalize %q = %q", input, got)
		}
	}
	for _, input := range []string{
		"opaque-provider-id",
		"67E55044-10B1-426F-9247-BB680E5FE0CG",
		"67E55044:10B1-426F-9247-BB680E5FE0C8",
		"URN:UUID:67E55044-10B1-426F-9247-BB680E5FE0C8",
	} {
		if got := CanonicalID(input); got != input {
			t.Errorf("changed opaque ID %q to %q", input, got)
		}
	}
}
