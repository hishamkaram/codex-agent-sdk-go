package runtimeprobe

import (
	"encoding/json"
	"path/filepath"
	"slices"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// SupportsTurnProgram checks for the optional program selector in generated schemas.
func SupportsTurnProgram(root string) (bool, error) {
	path := filepath.Join(root, "v2", "TurnStartParams.json")
	var schema struct {
		runtimeObjectSchema
		Definitions map[string]runtimePropertySchema `json:"definitions"`
	}
	found, err := readRuntimeSchema(path, &schema)
	if err != nil || !found {
		return false, err
	}
	raw, ok := schema.Properties["cyberAccessProgram"]
	if !ok || !schemaTypesEqual(schema.Type, "object") || slices.Contains(schema.Required, "cyberAccessProgram") {
		return false, nil
	}
	var property struct {
		AnyOf []runtimePropertySchema `json:"anyOf"`
	}
	if decodeErr := json.Unmarshal(raw, &property); decodeErr != nil {
		return false, decodeErr
	}
	enum := schema.Definitions["CyberAccessProgram"]
	supported := schemaTypesEqual(enum.Type, "string") &&
		equalStringSet(enum.Enum, []string{
			string(types.CyberAccessProgramStandard),
			string(types.CyberAccessProgramDaybreakBlue),
			string(types.CyberAccessProgramDaybreakRed),
		}) && hasTurnProgramReference(property.AnyOf)
	return supported, nil
}

func hasTurnProgramReference(variants []runtimePropertySchema) bool {
	if len(variants) != 2 {
		return false
	}
	first, second := variants[0], variants[1]
	if schemaTypesEqual(first.Type, "null") {
		first, second = second, first
	}
	return first.Ref == "#/definitions/CyberAccessProgram" && len(first.Type) == 0 && len(first.Enum) == 0 &&
		second.Ref == "" && len(second.Enum) == 0 && schemaTypesEqual(second.Type, "null")
}
