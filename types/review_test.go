package types

import (
	"encoding/json"
	"testing"
)

func TestReviewTargetsMarshalOnlyVariantFields(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		target ReviewTarget
		wire   string
	}{
		{"working tree", ReviewTargetUncommittedChanges(), `{"type":"uncommittedChanges"}`},
		{"base branch", ReviewTargetBaseBranch("main"), `{"type":"baseBranch","branch":"main"}`},
		{"commit", ReviewTargetCommit("abc123", "Update docs"), `{"type":"commit","sha":"abc123","title":"Update docs"}`},
		{"untitled commit", ReviewTargetCommit("abc123", ""), `{"type":"commit","sha":"abc123"}`},
		{"custom", ReviewTargetCustom("Check formatting."), `{"type":"custom","instructions":"Check formatting."}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(tc.target)
			if err != nil || string(got) != tc.wire {
				t.Fatalf("review target = %s, err = %v; want %s", got, err, tc.wire)
			}
		})
	}
}
