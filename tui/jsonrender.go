package tui

import (
	"encoding/json"
	"fmt"
	"io"
)

const (
	jsonRenderOSCPrefix = "\x1b]9999;json-render;"
	jsonRenderOSCBEL    = "\x07"
)

// EmitJSONRenderSpec writes a json-render UI spec as an OSC escape sequence
// to the given writer (typically os.Stdout when running inside a hosted PTY).
// The host server intercepts these sequences, strips them from terminal output,
// and forwards them to the web client as structured WebSocket messages.
func EmitJSONRenderSpec(w io.Writer, spec any) error {
	data, err := json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("json-render marshal: %w", err)
	}
	_, err = fmt.Fprintf(w, "%s%s%s", jsonRenderOSCPrefix, data, jsonRenderOSCBEL)
	return err
}

// JSONRenderElement represents a single element in a json-render UI tree.
type JSONRenderElement struct {
	Type     string            `json:"type"`
	Props    map[string]any    `json:"props"`
	Children []string          `json:"children,omitempty"`
}

// JSONRenderSpec is a complete json-render UI specification.
type JSONRenderSpec struct {
	Root     string                       `json:"root"`
	Elements map[string]JSONRenderElement `json:"elements"`
}

// NewJSONRenderCard creates a simple card spec with a title and text content.
func NewJSONRenderCard(title, content string) JSONRenderSpec {
	return JSONRenderSpec{
		Root: "card",
		Elements: map[string]JSONRenderElement{
			"card": {
				Type: "Card",
				Props: map[string]any{
					"title":       title,
					"description": nil,
					"variant":     "accent",
				},
				Children: []string{"text"},
			},
			"text": {
				Type: "Text",
				Props: map[string]any{
					"content": content,
					"variant": "default",
					"size":    "md",
				},
			},
		},
	}
}

// NewJSONRenderMetrics creates a metrics dashboard spec from label/value pairs.
func NewJSONRenderMetrics(title string, metrics []struct{ Label, Value string }) JSONRenderSpec {
	spec := JSONRenderSpec{
		Root: "stack",
		Elements: map[string]JSONRenderElement{
			"stack": {
				Type: "Stack",
				Props: map[string]any{
					"direction": "vertical",
					"gap":       "md",
					"align":     nil,
				},
				Children: []string{"heading", "metrics"},
			},
			"heading": {
				Type: "Heading",
				Props: map[string]any{
					"text":  title,
					"level": "2",
				},
			},
			"metrics": {
				Type: "Stack",
				Props: map[string]any{
					"direction": "horizontal",
					"gap":       "md",
					"align":     nil,
				},
			},
		},
	}

	children := make([]string, 0, len(metrics))
	for i, m := range metrics {
		key := fmt.Sprintf("m%d", i)
		children = append(children, key)
		spec.Elements[key] = JSONRenderElement{
			Type: "Metric",
			Props: map[string]any{
				"label":  m.Label,
				"value":  m.Value,
				"change": nil,
				"trend":  nil,
			},
		}
	}
	spec.Elements["metrics"] = JSONRenderElement{
		Type:     spec.Elements["metrics"].Type,
		Props:    spec.Elements["metrics"].Props,
		Children: children,
	}

	return spec
}
