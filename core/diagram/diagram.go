package diagram

import (
	"fmt"
	"regexp"
	"strings"
)

type Node struct {
	ID, Label string
}

type Edge struct {
	From, To, Label string
	Dashed          bool
}

type Model struct {
	Kind, Direction string
	Nodes           []Node
	Edges           []Edge
}

var (
	flowNode     = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_-]*)(?:\[([^\]]+)\]|\(([^)]+)\)|\{([^}]+)\})?$`)
	flowEdge     = regexp.MustCompile(`^(.+?)\s*-->(?:\|([^|]+)\|)?\s*(.+)$`)
	sequenceEdge = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_-]*?)\s*(-->>|->>|-->|->)\s*([A-Za-z][A-Za-z0-9_-]*)\s*:\s*(.+)$`)
)

func Parse(source string) (Model, error) {
	var model Model
	lines := strings.Split(source, "\n")
	if len(lines) > 160 {
		return model, fmt.Errorf("diagram exceeds 160 lines")
	}
	for index, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "%%") {
			continue
		}
		if len(line) > 512 {
			return model, fmt.Errorf("line %d exceeds 512 bytes", index+1)
		}
		if model.Kind == "" {
			parts := strings.Fields(line)
			switch parts[0] {
			case "flowchart", "graph":
				if len(parts) != 2 || !validDirection(parts[1]) {
					return model, fmt.Errorf("line %d: unsupported flowchart direction", index+1)
				}
				model.Kind, model.Direction = "flowchart", parts[1]
			case "sequenceDiagram":
				if len(parts) != 1 {
					return model, fmt.Errorf("line %d: unsupported sequence header", index+1)
				}
				model.Kind = "sequence"
			default:
				return model, fmt.Errorf("line %d: unsupported Mermaid construct %q", index+1, line)
			}
			continue
		}
		switch model.Kind {
		case "flowchart":
			if edge := flowEdge.FindStringSubmatch(line); edge != nil {
				from, err := model.addFlowNode(strings.TrimSpace(edge[1]))
				if err != nil {
					return model, fmt.Errorf("line %d: %w", index+1, err)
				}
				to, err := model.addFlowNode(strings.TrimSpace(edge[3]))
				if err != nil {
					return model, fmt.Errorf("line %d: %w", index+1, err)
				}
				model.Edges = append(model.Edges, Edge{From: from, To: to, Label: strings.TrimSpace(edge[2])})
			} else if _, err := model.addFlowNode(line); err != nil {
				return model, fmt.Errorf("line %d: %w", index+1, err)
			}
		case "sequence":
			edge := sequenceEdge.FindStringSubmatch(line)
			if edge == nil {
				return model, fmt.Errorf("line %d: unsupported Mermaid construct %q", index+1, line)
			}
			model.addNode(edge[1], edge[1])
			model.addNode(edge[3], edge[3])
			model.Edges = append(model.Edges, Edge{From: edge[1], To: edge[3], Label: strings.TrimSpace(edge[4]), Dashed: strings.HasPrefix(edge[2], "--")})
		}
		if len(model.Nodes) > 64 || len(model.Edges) > 128 {
			return model, fmt.Errorf("diagram exceeds 64 nodes or 128 edges")
		}
	}
	if model.Kind == "" {
		return model, fmt.Errorf("empty Mermaid block")
	}
	return model, nil
}

func validDirection(direction string) bool {
	switch direction {
	case "TD", "TB", "BT", "LR", "RL":
		return true
	}
	return false
}

func (model *Model) addNode(id, label string) {
	for index := range model.Nodes {
		if model.Nodes[index].ID == id {
			if label != id {
				model.Nodes[index].Label = label
			}
			return
		}
	}
	model.Nodes = append(model.Nodes, Node{ID: id, Label: label})
}

func (model *Model) addFlowNode(source string) (string, error) {
	match := flowNode.FindStringSubmatch(source)
	if match == nil {
		return "", fmt.Errorf("unsupported Mermaid construct %q", source)
	}
	label := match[1]
	for _, candidate := range match[2:] {
		if candidate != "" {
			label = candidate
			break
		}
	}
	model.addNode(match[1], label)
	return match[1], nil
}

func (model Model) label(id string) string {
	for _, node := range model.Nodes {
		if node.ID == id {
			return node.Label
		}
	}
	return id
}

func (model Model) Terminal() string {
	var output strings.Builder
	for _, edge := range model.Edges {
		if model.Kind == "sequence" {
			output.WriteString(edge.From)
		} else {
			output.WriteString("[" + model.label(edge.From) + "]")
		}
		connector := " ──▶ "
		if edge.Dashed {
			connector = " - -▶ "
		}
		if edge.Label != "" {
			connector = " ── " + edge.Label + " ──▶ "
		}
		output.WriteString(connector)
		if model.Kind == "sequence" {
			output.WriteString(edge.To)
		} else {
			output.WriteString("[" + model.label(edge.To) + "]")
		}
		output.WriteByte('\n')
	}
	if len(model.Edges) == 0 {
		for _, node := range model.Nodes {
			output.WriteString("[" + node.Label + "]\n")
		}
	}
	return strings.TrimSuffix(output.String(), "\n")
}
