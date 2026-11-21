package commands

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestListSkillsUsesDefaultCwd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cwd  string
		want map[string]any
	}{
		{
			name: "empty options",
			want: map[string]any{},
		},
		{
			name: "default cwd",
			cwd:  "/repo/subdir",
			want: map[string]any{"cwds": []string{"/repo/subdir"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var called bool
			send := func(_ context.Context, callerName, method string, params any) (json.RawMessage, error) {
				called = true
				if callerName != "ListSkills" || method != "skills/list" {
					t.Fatalf("caller/method = %q/%q, want ListSkills/skills/list", callerName, method)
				}
				if !reflect.DeepEqual(params, tt.want) {
					t.Fatalf("params = %#v, want %#v", params, tt.want)
				}
				return json.RawMessage(`{"data":[]}`), nil
			}
			if _, err := ListSkills(t.Context(), send, tt.cwd); err != nil {
				t.Fatal(err)
			}
			if !called {
				t.Fatal("ListSkills did not send its request")
			}
		})
	}
}
