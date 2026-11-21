package types

import "fmt"

// ThreadPolicyMismatchError reports that the app-server created a thread
// without confirming a requested workspace-write policy. The SDK does not
// register the thread. Callers can use ThreadID to inspect or archive it.
type ThreadPolicyMismatchError struct {
	ThreadID string
	Field    string
}

func (e *ThreadPolicyMismatchError) Error() string {
	return fmt.Sprintf("codex: thread %q created without confirming requested workspace-write policy (%s)", e.ThreadID, e.Field)
}
