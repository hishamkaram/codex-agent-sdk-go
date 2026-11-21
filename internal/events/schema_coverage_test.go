package events

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

type schemaVariant struct {
	Title         string                     `json:"title"`
	Required      []string                   `json:"required"`
	Properties    map[string]json.RawMessage `json:"properties"`
	Discriminator string                     `json:"-"`
}

func threadItemVariants(t *testing.T, doc schemaDoc) []schemaVariant {
	t.Helper()

	var union struct {
		OneOf []schemaVariant `json:"oneOf"`
	}
	if err := json.Unmarshal(doc.Definitions["ThreadItem"], &union); err != nil {
		t.Fatalf("unmarshal schema ThreadItem: %v", err)
	}
	if len(union.OneOf) == 0 {
		t.Fatal("schema ThreadItem.oneOf is empty")
	}

	discriminators := make(map[string]bool, len(union.OneOf))
	for i := range union.OneOf {
		item := &union.OneOf[i]
		var discriminator struct {
			Enum []string `json:"enum"`
		}
		if err := json.Unmarshal(item.Properties["type"], &discriminator); err != nil {
			t.Fatalf("unmarshal schema %s.type: %v", item.Title, err)
		}
		if item.Title == "" || len(discriminator.Enum) != 1 || discriminator.Enum[0] == "" {
			t.Fatalf("invalid ThreadItem variant %q with discriminator %v", item.Title, discriminator.Enum)
		}
		item.Discriminator = discriminator.Enum[0]
		if discriminators[item.Discriminator] {
			t.Fatalf("duplicate ThreadItem discriminator %q", item.Discriminator)
		}
		discriminators[item.Discriminator] = true
	}
	return union.OneOf
}

// TestSchemaCoverage guards required wire fields against the concrete types
// returned by ParseItem. The discriminator itself lives in the envelope.
// Sleep has a raw policy and is checked by the item dispatch coverage test.
func TestSchemaCoverage(t *testing.T) {
	t.Parallel()

	for _, variant := range threadItemVariants(t, readSchemaDoc(t, vendoredV2Schema)) {
		payload := schemaItemPayload(t, variant.Discriminator)
		item, err := ParseItem(payload)
		if err != nil {
			t.Fatalf("ParseItem %q: %v", variant.Discriminator, err)
		}
		if variant.Discriminator == "sleep" {
			continue
		}
		if _, raw := item.(*types.UnknownItem); raw {
			t.Fatalf("missing typed parser for %q", variant.Discriminator)
		}
		goType := reflect.TypeOf(item).Elem()
		gotTags := make(map[string]bool, goType.NumField())
		for i := 0; i < goType.NumField(); i++ {
			name, _, _ := strings.Cut(goType.Field(i).Tag.Get("json"), ",")
			gotTags[name] = true
		}
		for _, property := range variant.Required {
			if property != "type" && !gotTags[property] {
				t.Errorf("missing Go field for required property %q on %s", property, variant.Title)
			}
		}
	}
}

// TestSchemaMetadataContracts pins the optional nullable fields surfaced by
// supported SDK APIs independently of JSON parser regressions.
func TestSchemaMetadataContracts(t *testing.T) {
	t.Parallel()

	doc := readSchemaDoc(t, vendoredV2Schema)
	metadata := map[string][]string{
		"CollabAgentToolCallThreadItem":      {"model", "reasoningEffort"},
		"SubAgentActivityThreadItem":         {"model", "reasoningEffort"},
		"McpToolCallThreadItem":              {"environmentId"},
		"FunctionCallOutputThreadItem":       {"namespace"},
		"McpServerStatus":                    {"environmentId"},
		"McpServerStatusUpdatedNotification": {"environmentId"},
	}
	for _, variant := range threadItemVariants(t, doc) {
		if properties, ok := metadata[variant.Title]; ok {
			assertOptionalNullableProperties(t, variant, properties)
			delete(metadata, variant.Title)
		}
	}
	for definition, properties := range metadata {
		var variant schemaVariant
		if err := json.Unmarshal(doc.Definitions[definition], &variant); err != nil {
			t.Fatalf("unmarshal schema %s: %v", definition, err)
		}
		variant.Title = definition
		assertOptionalNullableProperties(t, variant, properties)
	}
}

func assertOptionalNullableProperties(t *testing.T, variant schemaVariant, propertyNames []string) {
	t.Helper()
	for _, name := range propertyNames {
		var property struct {
			Type  []string `json:"type"`
			AnyOf []struct {
				Type string `json:"type"`
			} `json:"anyOf"`
		}
		if err := json.Unmarshal(variant.Properties[name], &property); err != nil {
			t.Errorf("unmarshal schema %s.%s: %v", variant.Title, name, err)
			continue
		}
		allowsNull := slices.Contains(property.Type, "null") || slices.ContainsFunc(property.AnyOf, func(member struct {
			Type string `json:"type"`
		},
		) bool {
			return member.Type == "null"
		})
		if !allowsNull || slices.Contains(variant.Required, name) {
			t.Errorf("schema %s.%s must be optional and nullable", variant.Title, name)
		}
	}
}
