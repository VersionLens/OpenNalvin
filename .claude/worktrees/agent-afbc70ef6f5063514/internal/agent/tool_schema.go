package agent

import (
	"context"
	"reflect"
	"strings"

	"charm.land/fantasy"
)

type describedAgentTool struct {
	base fantasy.AgentTool
	info fantasy.ToolInfo
}

func (t *describedAgentTool) Info() fantasy.ToolInfo {
	return t.info
}

func (t *describedAgentTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	return t.base.Run(ctx, call)
}

func (t *describedAgentTool) ProviderOptions() fantasy.ProviderOptions {
	return t.base.ProviderOptions()
}

func (t *describedAgentTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	t.base.SetProviderOptions(opts)
}

func newParallelAgentTool[TInput any](
	name string,
	description string,
	fn func(ctx context.Context, input TInput, call fantasy.ToolCall) (fantasy.ToolResponse, error),
) fantasy.AgentTool {
	base := fantasy.NewParallelAgentTool(name, description, fn)
	info := base.Info()
	info.Parameters = enrichToolParametersWithDescriptions(info.Parameters, typeOf[TInput]())
	return &describedAgentTool{
		base: base,
		info: info,
	}
}

func typeOf[T any]() reflect.Type {
	return reflect.TypeOf((*T)(nil)).Elem()
}

func enrichToolParametersWithDescriptions(parameters map[string]any, inputType reflect.Type) map[string]any {
	if len(parameters) == 0 {
		return parameters
	}
	out, ok := enrichSchemaNode(deepCloneMap(parameters), inputType).(map[string]any)
	if !ok {
		return parameters
	}
	return out
}

func enrichSchemaNode(node any, valueType reflect.Type) any {
	if valueType == nil {
		return node
	}
	for valueType.Kind() == reflect.Pointer {
		valueType = valueType.Elem()
	}

	switch valueType.Kind() {
	case reflect.Struct:
		properties, ok := node.(map[string]any)
		if !ok {
			return node
		}
		for i := 0; i < valueType.NumField(); i++ {
			field := valueType.Field(i)
			if !field.IsExported() {
				continue
			}
			fieldName, include := schemaFieldName(field)
			if !include {
				continue
			}
			child, ok := properties[fieldName]
			if !ok {
				continue
			}
			childMap, ok := child.(map[string]any)
			if !ok {
				continue
			}
			if desc := fieldDescription(field); desc != "" {
				if _, exists := childMap["description"]; !exists {
					childMap["description"] = desc
				}
			}
			properties[fieldName] = enrichSchemaNode(childMap, field.Type)
		}
		return properties
	case reflect.Slice, reflect.Array:
		nodeMap, ok := node.(map[string]any)
		if !ok {
			return node
		}
		if items, ok := nodeMap["items"]; ok {
			nodeMap["items"] = enrichSchemaNode(items, valueType.Elem())
		}
		return nodeMap
	case reflect.Map:
		nodeMap, ok := node.(map[string]any)
		if !ok {
			return node
		}
		if valueType.Key().Kind() == reflect.String {
			if wildcard, ok := nodeMap["*"]; ok {
				nodeMap["*"] = enrichSchemaNode(wildcard, valueType.Elem())
			}
		}
		return nodeMap
	default:
		return node
	}
}

func schemaFieldName(field reflect.StructField) (string, bool) {
	jsonTag := field.Tag.Get("json")
	if jsonTag == "-" {
		return "", false
	}
	if jsonTag == "" {
		return snakeCaseFieldName(field.Name), true
	}
	parts := strings.Split(jsonTag, ",")
	if parts[0] == "" {
		return snakeCaseFieldName(field.Name), true
	}
	return parts[0], true
}

func fieldDescription(field reflect.StructField) string {
	if desc := strings.TrimSpace(field.Tag.Get("jsonschema_description")); desc != "" {
		return desc
	}
	return strings.TrimSpace(field.Tag.Get("description"))
}

func snakeCaseFieldName(value string) string {
	var result strings.Builder
	for i, r := range value {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result.WriteByte('_')
		}
		result.WriteRune(r)
	}
	return strings.ToLower(result.String())
}

func deepCloneMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = deepCloneValue(value)
	}
	return out
}

func deepCloneValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return deepCloneMap(v)
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = deepCloneValue(v[i])
		}
		return out
	default:
		return value
	}
}
