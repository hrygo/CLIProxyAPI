package responsestools

import (
	"crypto/sha256"
	"encoding/json"
	"slices"
)

// PositionAnalysis describes Lite declaration scope without retaining client
// content, tool names, schemas, or identity fingerprints in diagnostics.
type PositionAnalysis struct {
	DeclarationIndices  []int `json:"declaration_indices,omitempty"`
	Interleaved         bool  `json:"interleaved"`
	SameIdentityChanged bool  `json:"same_identity_changed"`
}

type TransformationLoss string

const LossAdditionalToolsPosition TransformationLoss = "additional_tools_position_lift"

// TransformationReport covers declaration position fidelity only. It does not
// certify schema, grammar, permission, or execution semantics.
type TransformationReport struct {
	Positions         PositionAnalysis     `json:"positions"`
	PositionPreserved bool                 `json:"position_preserved"`
	Losses            []TransformationLoss `json:"losses,omitempty"`
}

// AnalyzeToolPositions compares declaration identities in their original order.
// Namespace wrappers are compared separately from leaves, so adding a sibling
// does not look like an update to an existing tool. Traversal is iterative to
// avoid adding a recursion limit to the native passthrough path.
func AnalyzeToolPositions(root map[string]any) PositionAnalysis {
	var analysis PositionAnalysis
	seen := make(map[string][sha256.Size]byte)
	type frame struct {
		tool any
		path []string
	}
	observe := func(tools []any) {
		stack := make([]frame, 0, len(tools))
		for i := len(tools) - 1; i >= 0; i-- {
			stack = append(stack, frame{tool: tools[i]})
		}
		for len(stack) > 0 {
			current := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			tool, ok := current.tool.(map[string]any)
			if !ok {
				continue
			}
			kind := stringField(tool, "type")
			name := stringField(tool, "name")
			if kind == "" {
				continue
			}
			identity := append(slices.Clone(current.path), kind, name)
			key, _ := json.Marshal(identity)
			metadata := tool
			if kind == NamespaceToolType {
				metadata = make(map[string]any, len(tool))
				for k, v := range tool {
					if k != "tools" {
						metadata[k] = v
					}
				}
				children, _ := tool["tools"].([]any)
				path := append(slices.Clone(current.path), name)
				for i := len(children) - 1; i >= 0; i-- {
					stack = append(stack, frame{tool: children[i], path: path})
				}
			}
			encoded, err := json.Marshal(metadata)
			if err != nil {
				continue
			}
			fingerprint := sha256.Sum256(encoded)
			if old, exists := seen[string(key)]; exists && old != fingerprint {
				analysis.SameIdentityChanged = true
			}
			seen[string(key)] = fingerprint
		}
	}
	tools, _ := root["tools"].([]any)
	observe(tools)
	input, _ := root["input"].([]any)
	conversationBefore := false
	declarationAfterConversation := false
	for i, raw := range input {
		item, ok := raw.(map[string]any)
		if ok && stringField(item, "type") == "additional_tools" {
			analysis.DeclarationIndices = append(analysis.DeclarationIndices, i)
			declarationAfterConversation = declarationAfterConversation || conversationBefore
			tools, _ := item["tools"].([]any)
			observe(tools)
		} else {
			if declarationAfterConversation {
				analysis.Interleaved = true
			}
			conversationBefore = true
		}
	}
	return analysis
}

func (report *TransformationReport) comparePositions(root map[string]any) {
	var indices []int
	input, _ := root["input"].([]any)
	for i, raw := range input {
		if item, ok := raw.(map[string]any); ok && stringField(item, "type") == "additional_tools" {
			indices = append(indices, i)
		}
	}
	report.PositionPreserved = slices.Equal(report.Positions.DeclarationIndices, indices)
	if !report.PositionPreserved {
		report.Losses = []TransformationLoss{LossAdditionalToolsPosition}
	}
}
