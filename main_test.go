package codex

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == turnProgramPeerName {
		os.Exit(runTurnProgramPeer())
	}
	goleak.VerifyTestMain(m)
}
