package codex

import (
	"encoding/json"
	"path/filepath"
	"slices"
)

const approvedPlanParamsDefinition = "ThreadPrepareApprovedPlanParams"

func schemaSupportsApprovedPlan(root string) (bool, error) {
	if supported, err := schemaHasApprovedPlanMethod(filepath.Join(root, "ClientRequest.json")); err != nil || !supported {
		return false, err
	}
	v2 := filepath.Join(root, "v2")
	if supported, err := schemaHasRequestProperties(filepath.Join(v2, approvedPlanParamsDefinition+".json"), approvedPlanParameterExpectations()...); err != nil || !supported {
		return false, err
	}
	return schemaHasRequestProperties(filepath.Join(v2, "ThreadPrepareApprovedPlanResponse.json"), requiredTypedProperty("prompt", "string"))
}

func approvedPlanParameterExpectations() []runtimePropertyExpectation {
	return []runtimePropertyExpectation{
		requiredTypedProperty("threadId", "string"),
		requiredTypedProperty("markdown", "string"),
		optionalTypedProperty("attachmentPath", "string", "null"),
	}
}

func schemaHasApprovedPlanMethod(path string) (bool, error) {
	var schema struct {
		Variants    []runtimeObjectSchema          `json:"oneOf"`
		Definitions map[string]runtimeObjectSchema `json:"definitions"`
	}
	if found, err := readRuntimeSchema(path, &schema); err != nil || !found {
		return false, err
	}
	params := schema.Definitions[approvedPlanParamsDefinition]
	if !equalStringSet(params.Required, []string{"threadId", "markdown"}) ||
		!objectSchemaMatches(params, approvedPlanParameterExpectations()...) {
		return false, nil
	}

	found := false
	for _, variant := range schema.Variants {
		var method runtimePropertySchema
		if json.Unmarshal(variant.Properties["method"], &method) != nil || !slices.Contains(method.Enum, prepareApprovedPlanMethod) {
			continue
		}
		if found || !approvedPlanMethodMatches(variant) {
			return false, nil
		}
		found = true
	}
	return found, nil
}

func approvedPlanMethodMatches(method runtimeObjectSchema) bool {
	return equalStringSet(method.Required, []string{"id", "method", "params"}) && objectSchemaMatches(
		method,
		runtimePropertyExpectation{name: "id", required: true, ref: "#/definitions/RequestId"},
		runtimePropertyExpectation{name: "method", required: true, types: []string{"string"}, enum: []string{prepareApprovedPlanMethod}},
		runtimePropertyExpectation{name: "params", required: true, ref: "#/definitions/" + approvedPlanParamsDefinition},
	)
}
