package transport

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWindowsCLIEmptyReplacementKeepsSystemRoot(t *testing.T) {
	const childArgument = "sdk57-empty-replacement-child"
	const parentOnly = "CODEX_SDK_TEST_PARENT_ONLY"
	if os.Args[len(os.Args)-1] == childArgument {
		if _, leaked := os.LookupEnv(parentOnly); leaked {
			t.Fatal("an inherited variable reached the replacement child")
		}
		root, present := os.LookupEnv("SYSTEMROOT")
		if !present || strings.TrimSpace(root) == "" {
			t.Fatal("SYSTEMROOT is required to launch the Windows child")
		}
		fmt.Println("verified replacement child")
		return
	}
	t.Setenv(parentOnly, "synthetic-value")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cli, err := ResolveCLI(executable, "", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	out, stderr, err := cli.Run(t.Context(), time.Second,
		"-test.run=^TestWindowsCLIEmptyReplacementKeepsSystemRoot$", "--", childArgument)
	if err != nil {
		t.Fatalf("native replacement child: %v (stdout=%q, stderr=%q)", err, out, stderr)
	}
	if !strings.Contains(out, "verified replacement child") {
		t.Fatalf("missing confirmation from replacement child: %q", out)
	}
}
