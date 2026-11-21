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

// ThreadResumeSettingsMismatchError reports that the app-server resumed a
// thread but its reply did not confirm the requested reasoning effort.
// Codex can retain an already loaded thread's settings. Apply resume
// overrides on a connection where the persisted thread has not been loaded.
type ThreadResumeSettingsMismatchError struct {
	ThreadID string
}

func (e *ThreadResumeSettingsMismatchError) Error() string {
	return fmt.Sprintf("codex: thread %q resumed without confirming requested reasoning effort; resume it when it is unloaded", e.ThreadID)
}
