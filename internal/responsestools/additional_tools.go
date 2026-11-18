package responsestools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// liftAdditionalTools adapts Lite declaration containers for portable native
// Responses routes. Conversation item identities and discovery history remain
// intact, but declaration positions are lost and reported by Prepare.
func liftAdditionalTools(root map[string]any, limits Limits) error {
	tools, _ := root["tools"].([]any)
	if value, exists := root["tools"]; exists && value != nil && tools == nil {
		return unprocessableError(ReasonUnsupportedProtocol, fmt.Errorf("tools must be an array"))
	}
	if err := ValidateToolArrayDepth(tools, 1, limits.MaxDepth); err != nil {
		return err
	}
	completeLiteNamespaceDescriptions(tools)
	input, _ := root["input"].([]any)
	remaining := make([]any, 0, len(input))
	for _, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok || stringField(item, "type") != "additional_tools" {
			remaining = append(remaining, raw)
			continue
		}
		additional, ok := item["tools"].([]any)
		if !ok {
			return unprocessableError(ReasonUnsupportedProtocol, fmt.Errorf("additional_tools requires a tools array"))
		}
		if err := ValidateToolArrayDepth(additional, 1, limits.MaxDepth); err != nil {
			return err
		}
		completeLiteNamespaceDescriptions(additional)
		var err error
		tools, err = mergeDeclarationArrays(tools, additional)
		if err != nil {
			return err
		}
	}
	root["tools"] = tools
	root["input"] = remaining
	return nil
}

// Lite emits an empty description for the default functions namespace.
// Portable native validators require a nonempty namespace description, so
// supply a neutral label while preserving nonempty client metadata.
func completeLiteNamespaceDescriptions(tools []any) {
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok || stringField(tool, "type") != NamespaceToolType {
			continue
		}
		if strings.TrimSpace(stringField(tool, "description")) == "" {
			tool["description"] = fmt.Sprintf("Tools in the %s namespace.", stringField(tool, "name"))
		}
		children, _ := tool["tools"].([]any)
		completeLiteNamespaceDescriptions(children)
	}
}

// Merge by declaration identity, preserving namespace containers until the
// normal custom/function adapter runs. Conflicting definitions fail locally
// rather than letting one declaration silently overwrite another.
func mergeDeclarationArrays(tools, additional []any) ([]any, error) {
	for _, raw := range additional {
		tool, ok := raw.(map[string]any)
		if !ok || stringField(tool, "type") == "" {
			return nil, unprocessableError(ReasonUnsupportedProtocol, fmt.Errorf("invalid additional tool declaration"))
		}
		found := false
		for _, existing := range tools {
			old, ok := existing.(map[string]any)
			if !ok || stringField(old, "type") != stringField(tool, "type") || stringField(old, "name") != stringField(tool, "name") {
				continue
			}
			found = true
			if stringField(tool, "type") == NamespaceToolType {
				children, ok := tool["tools"].([]any)
				oldChildren, oldOK := old["tools"].([]any)
				if !ok || !oldOK {
					return nil, unprocessableError(ReasonUnsupportedProtocol, fmt.Errorf("namespace requires a tools array"))
				}
				// Wrapper metadata must agree as well as leaf schemas.
				oldMeta, newMeta := make(map[string]any), make(map[string]any)
				for k, v := range old {
					if k != "tools" {
						oldMeta[k] = v
					}
				}
				for k, v := range tool {
					if k != "tools" {
						newMeta[k] = v
					}
				}
				if !sameDeclaration(oldMeta, newMeta) {
					return nil, unprocessableError(ReasonAmbiguousIdentity, fmt.Errorf("conflicting namespace declaration"))
				}
				merged, err := mergeDeclarationArrays(oldChildren, children)
				if err != nil {
					return nil, err
				}
				old["tools"] = merged
			} else if !sameDeclaration(old, tool) {
				return nil, unprocessableError(ReasonAmbiguousIdentity, fmt.Errorf("conflicting additional tool declaration"))
			}
			break
		}
		if !found {
			tools = append(tools, raw)
		}
	}
	return tools, nil
}

func sameDeclaration(a, b map[string]any) bool {
	x, err := json.Marshal(a)
	if err != nil {
		return false
	}
	y, err := json.Marshal(b)
	return err == nil && bytes.Equal(x, y)
}
