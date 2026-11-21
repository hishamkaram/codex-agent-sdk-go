package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A symlink to this test binary gives the SDK a direct native executable,
// so the fixture exercises the same Linux image proof as a real connection.
const turnProgramPeerName = "sdk57-turn-program-peer"

func runTurnProgramPeer() int {
	dir := filepath.Dir(os.Args[0])
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "--dangerously-bypass-hook-trust" {
		args = args[1:]
	}
	if err := serveTurnProgramPeer(dir, args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func serveTurnProgramPeer(dir string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing test CLI command")
	}
	if err := recordTurnProgramPeerEnv(dir, args); err != nil {
		return err
	}
	switch args[0] {
	case "--version":
		_, err := fmt.Fprintln(os.Stdout, "codex 0.171.0")
		return err
	case "--help":
		_, err := fmt.Fprintln(os.Stdout, "-s, --sandbox <MODE>\n  [possible values: read-only]\n-a, --ask-for-approval <POLICY>\n  - never: no requests")
		return err
	case "app-server":
		if len(args) > 1 && args[1] == "generate-json-schema" {
			return generateTurnProgramPeerSchema(dir, args[2:])
		}
		return serveTurnProgramPeerRPC(dir)
	default:
		return fmt.Errorf("unknown test CLI command")
	}
}

func recordTurnProgramPeerEnv(dir string, args []string) error {
	arg2 := ""
	if len(args) > 1 {
		arg2 = args[1]
	}
	fields := make([]string, 0, 7)
	fields = append(fields, args[0], arg2)
	for _, key := range []string{"CODEX_HOME", "CODEX_SDK_HOOK_SOCKET", "SDK57_PUBLIC_SELECTION", "SDK57_PUBLIC_PARENT_ONLY", "HOME"} {
		value, ok := os.LookupEnv(key)
		if !ok {
			value = "unset"
		}
		fields = append(fields, value)
	}
	return appendPeerLog(filepath.Join(dir, "environment"), strings.Join(fields, "|")+"\n")
}

func generateTurnProgramPeerSchema(dir string, args []string) error {
	if len(args) == 1 && args[0] == "--help" {
		_, err := fmt.Fprintln(os.Stdout, " --out PATH\n --experimental")
		return err
	}
	if len(args) != 3 || args[0] != "--experimental" || args[1] != "--out" {
		return fmt.Errorf("test schema generator requires experimental mode")
	}
	if err := appendPeerLog(filepath.Join(dir, "schema-calls"), "experimental\n"); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, "schema-block")); err == nil {
		// The test cancels this probe after seeing the marker above. The
		// deadline here handles a test failure or interrupted test runner.
		time.Sleep(30 * time.Second)
	}
	body, err := os.ReadFile(filepath.Join(dir, "schema.json"))
	if err != nil {
		return err
	}
	out := filepath.Join(args[2], "v2")
	if mkdirErr := os.MkdirAll(out, 0o700); mkdirErr != nil {
		return mkdirErr
	}
	if err := os.WriteFile(filepath.Join(out, "TurnStartParams.json"), body, 0o600); err != nil {
		return err
	}
	return copyApprovedPlanPeerSchema(dir, args[2])
}

type turnProgramPeerRequest struct {
	ID     int    `json:"id"`
	Method string `json:"method"`
	Params struct {
		ThreadID string `json:"threadId"`
		Program  string `json:"cyberAccessProgram"`
		Markdown string `json:"markdown"`
	} `json:"params"`
}

func serveTurnProgramPeerRPC(dir string) error {
	scanner := bufio.NewScanner(os.Stdin)
	starts := 0
	for scanner.Scan() {
		line := scanner.Text()
		if err := appendPeerLog(filepath.Join(dir, "calls.jsonl"), line+"\n"); err != nil {
			return err
		}
		var frame turnProgramPeerRequest
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			return err
		}
		if frame.Method == "thread/start" {
			starts++
		}
		if err := replyTurnProgramPeer(frame, starts); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func replyTurnProgramPeer(frame turnProgramPeerRequest, starts int) error {
	id := frame.ID
	switch frame.Method {
	case "initialize":
		_, err := fmt.Fprintf(os.Stdout, "{\"id\":%d,\"result\":{}}\n", id)
		return err
	case "thread/start":
		_, err := fmt.Fprintf(os.Stdout, "{\"id\":%d,\"result\":{\"thread\":{\"id\":\"t%d\"}}}\n", id, starts)
		return err
	case "thread/prepareApprovedPlan":
		return replyApprovedPlanPeer(frame)
	case "turn/start":
		if frame.Params.Program == "daybreakRed" {
			_, err := fmt.Fprintf(os.Stdout, "{\"id\":%d,\"error\":{\"code\":-32123,\"message\":\"test provider denied requested program\"}}\n", id)
			return err
		}
		_, err := fmt.Fprintf(os.Stdout, `{"method":"turn/started","params":{"threadId":%q,"turn":{"id":"turn-%d"}}}
{"id":%d,"result":{"turn":{"id":"turn-%d"}}}
{"method":"turn/completed","params":{"threadId":%q,"turn":{"id":"turn-%d","status":"completed"}}}
`, frame.Params.ThreadID, id, id, id, frame.Params.ThreadID, id)
		return err
	default:
		return nil
	}
}

func appendPeerLog(path, text string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, writeErr := file.WriteString(text); writeErr != nil {
		_ = file.Close()
		return writeErr
	}
	return file.Close()
}
