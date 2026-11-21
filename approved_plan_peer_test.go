package codex

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	approvedPlanPeerPrompt    = "\n Implement the provider-approved plan. \n"
	approvedPlanPeerHold      = "wait without a preparation response"
	approvedPlanPeerReject    = "reject this preparation"
	approvedPlanPeerMalformed = "acknowledge with an invalid prompt key"
	approvedPlanPeerReceived  = "fixture/approved-plan-received"
)

// Reduced independently from the Codex 0.171.0 experimental output.
const approvedPlanPeerParameters = `{"type":"object","required":["markdown","threadId"],"properties":{
	"threadId":{"type":"string"},
	"markdown":{"type":"string"},
	"attachmentPath":{"type":["string","null"]}
}}`

const approvedPlanPeerResponse = `{"type":"object","required":["prompt"],"properties":{"prompt":{"type":"string"}}}`

func approvedPlanPeerSchemas() map[string]string {
	return map[string]string{
		"ClientRequest.json": fmt.Sprintf(`{"oneOf":[{
			"type":"object","required":["id","method","params"],"properties":{
				"id":{"$ref":"#/definitions/RequestId"},
				"method":{"type":"string","enum":["thread/prepareApprovedPlan"]},
				"params":{"$ref":"#/definitions/ThreadPrepareApprovedPlanParams"}
			}
		}],"definitions":{"RequestId":{"type":"integer"},"ThreadPrepareApprovedPlanParams":%s}}`, approvedPlanPeerParameters),
		"v2/ThreadPrepareApprovedPlanParams.json":   approvedPlanPeerParameters,
		"v2/ThreadPrepareApprovedPlanResponse.json": approvedPlanPeerResponse,
	}
}

func copyApprovedPlanPeerSchema(fixtureDir, outDir string) error {
	for path := range approvedPlanPeerSchemas() {
		data, err := os.ReadFile(filepath.Join(fixtureDir, "approved-plan", path))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(outDir, path), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func replyApprovedPlanPeer(frame turnProgramPeerRequest) error {
	var err error
	switch frame.Params.Markdown {
	case approvedPlanPeerHold:
		_, err = fmt.Fprintf(os.Stdout, "{\"method\":%q,\"params\":{\"threadId\":%q}}\n", approvedPlanPeerReceived, frame.Params.ThreadID)
	case approvedPlanPeerReject:
		_, err = fmt.Fprintf(os.Stdout, "{\"id\":%d,\"error\":{\"code\":-32177,\"message\":\"fixture rejected preparation\",\"data\":{\"reason\":\"fixture\"}}}\n", frame.ID)
	case approvedPlanPeerMalformed:
		_, err = fmt.Fprintf(os.Stdout, "{\"id\":%d,\"result\":{\"Prompt\":\"wrong field name\"}}\n", frame.ID)
	default:
		_, err = fmt.Fprintf(os.Stdout, "{\"id\":%d,\"result\":{\"prompt\":%q}}\n", frame.ID, approvedPlanPeerPrompt)
	}
	return err
}
