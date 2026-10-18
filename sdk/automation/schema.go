// Package automation implements the VaultDock automation schema: the JSON
// document format, its catalogue of triggers/conditions/actions, validation,
// template rendering and the evaluation engine.
//
// The same package is compiled into the server (validation, dry runs,
// storage) and the desktop client (live evaluation against local file
// events), so the visual editor, the JSON format and the runtime always agree.
package automation

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// SchemaID identifies version 1 of the automation document format.
const SchemaID = "vaultdock.automation/v1"

// Limits keep documents small and evaluation bounded.
const (
	MaxNodes        = 200
	MaxEdges        = 400
	MaxNameLen      = 120
	MaxDescLen      = 2000
	MaxRuleDepth    = 5
	MaxRulesInGroup = 50
	MaxStringParam  = 1024
	MaxListItems    = 100
)

// Node types.
const (
	TypeTrigger   = "trigger"
	TypeCondition = "condition"
	TypeAction    = "action"
)

// Edge ports.
const (
	PortNext  = "next"
	PortTrue  = "true"
	PortFalse = "false"
)

// Automation is the portable automation document.
type Automation struct {
	Schema      string `json:"schema"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
	// Priority orders automations that act on the same file. Higher runs
	// later and wins conflicting decisions.
	Priority int    `json:"priority,omitempty"`
	Nodes    []Node `json:"nodes"`
	Edges    []Edge `json:"edges"`
}

// Node is a single trigger, condition or action.
type Node struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Kind     string         `json:"kind"`
	Label    string         `json:"label,omitempty"`
	Params   map[string]any `json:"params,omitempty"`
	Position *Position      `json:"position,omitempty"`
}

// Position is optional layout information for the visual editor.
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Edge connects the output port of one node to another node.
// Triggers and actions use the "next" port (default); conditions use
// "true" or "false".
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Port string `json:"port,omitempty"`
}

// Parse decodes a document strictly (unknown top-level and node fields are
// rejected) and validates it. The returned automation has normalised params.
func Parse(data []byte) (*Automation, *ValidationResult) {
	var a Automation
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return nil, &ValidationResult{Errors: []Issue{{Path: "$", Message: jsonErrorMessage(err)}}}
	}
	if dec.More() {
		return nil, &ValidationResult{Errors: []Issue{{Path: "$", Message: "unexpected data after the JSON document"}}}
	}
	res := Validate(&a)
	if !res.Valid() {
		return &a, res
	}
	return &a, res
}

func jsonErrorMessage(err error) string {
	switch e := err.(type) {
	case *json.SyntaxError:
		return fmt.Sprintf("invalid JSON at byte %d: %v", e.Offset, e)
	case *json.UnmarshalTypeError:
		field := e.Field
		if field == "" {
			field = "document"
		}
		return fmt.Sprintf("%s: expected %s but got %s", field, e.Type.String(), e.Value)
	default:
		return "invalid JSON: " + err.Error()
	}
}

// NodeByID returns the node with the given id.
func (a *Automation) NodeByID(id string) *Node {
	for i := range a.Nodes {
		if a.Nodes[i].ID == id {
			return &a.Nodes[i]
		}
	}
	return nil
}

// Clone returns a deep copy via JSON round-trip.
func (a *Automation) Clone() *Automation {
	b, _ := json.Marshal(a)
	var c Automation
	_ = json.Unmarshal(b, &c)
	return &c
}
