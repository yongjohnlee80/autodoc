package rpc

import (
	"context"
	"fmt"

	"github.com/yongjohnlee80/autodoc/core/module"
)

// MaxScaffoldFiles is the most files one workspace.scaffold call takes; more is the call's error,
// before anything is written
// (docs/decisions/contract.md#the-daemon-writes-the-scaffold-create-only).
const MaxScaffoldFiles = 64

// ScaffoldFile is one file a KB type creates in a workspace.
type ScaffoldFile struct {
	Path      string `json:"path"`                // relative to the root, slash-separated
	Content   []byte `json:"content"`             // UTF-8 text
	Managed   bool   `json:"managed,omitempty"`   // may replace an older copy: honoured only for the root KB_OPERATIONS.md
	Immutable string `json:"immutable,omitempty"` // the immutable folder this is the descriptor of: skipped unless that folder is absent or empty
}

// The outcomes of one scaffolded file.
const (
	ScaffoldCreated = "created" // it did not exist, and now does
	ScaffoldKept    = "kept"    // it existed, and is as it was
	ScaffoldUpdated = "updated" // the managed file, older than the build's, replaced
	ScaffoldSkipped = "skipped" // an immutable folder's descriptor, the folder present and not empty
	ScaffoldRefused = "refused" // not written; Err says why
)

// ScaffoldResult is one file's outcome. A file's refusal is its result, never the call's error,
// so what landed before it is reported.
type ScaffoldResult struct {
	Path    string `json:"path"`
	Outcome string `json:"outcome"`
	Err     string `json:"error,omitempty"`
}

// scaffolder creates a KB type's files in a workspace, through the workspace's own rooted
// filesystem and not its index's admission (the daemon's Workspaces).
type scaffolder interface {
	Scaffold(ctx context.Context, name string, files []ScaffoldFile) ([]ScaffoldResult, error)
}

type workspaceScaffold struct {
	Workspace string        `param:"workspace"`
	Files     scaffoldFiles `param:"files" doc:"[{path, content, managed?, immutable?}], at most 64"`
}

// scaffoldFiles decodes the files param: a list of maps, each with a string path, content as a
// string or bytes, and an optional bool managed and string immutable.
type scaffoldFiles []ScaffoldFile

func (*scaffoldFiles) ParamType() string { return "list" }

func (fs *scaffoldFiles) DecodeParam(v any) error {
	list, ok := v.([]any)
	if !ok {
		return invalid("files: a list of {path, content, managed?, immutable?}")
	}
	if len(list) > MaxScaffoldFiles {
		return invalid(fmt.Sprintf("files: at most %d a call", MaxScaffoldFiles))
	}
	out := make(scaffoldFiles, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return invalid(fmt.Sprintf("files[%d]: a map", i))
		}
		var f ScaffoldFile
		if f.Path, ok = m["path"].(string); !ok {
			return invalid(fmt.Sprintf("files[%d].path: a string", i))
		}
		switch c := m["content"].(type) {
		case string:
			f.Content = []byte(c)
		case []byte:
			f.Content = c
		default:
			return invalid(fmt.Sprintf("files[%d].content: a string", i))
		}
		if v, set := m["managed"]; set && v != nil {
			if f.Managed, ok = v.(bool); !ok {
				return invalid(fmt.Sprintf("files[%d].managed: a bool", i))
			}
		}
		if v, set := m["immutable"]; set && v != nil {
			if f.Immutable, ok = v.(string); !ok {
				return invalid(fmt.Sprintf("files[%d].immutable: a string", i))
			}
		}
		out = append(out, f)
	}
	*fs = out
	return nil
}

// scaffoldVerb is workspace.scaffold
// (docs/decisions/contract.md#the-daemon-writes-the-scaffold-create-only).
func (s *Server) scaffoldVerb() module.Verb {
	return module.Handle(module.Contract{Verb: "workspace.scaffold", Effect: module.EffectWrite, Since: 27,
		Audience: module.AudienceUser, Result: "[{path, outcome, error?}]",
		Errors: []int64{CodeNoSuchWorkspace, CodeUnsupported},
		Doc: "Creates a KB type's files under the workspace's root, create-only: a file that exists is kept. " +
			"The root KB_OPERATIONS.md, marked managed, replaces a copy that declares an older autodoc_version. " +
			"An immutable folder's descriptor is skipped when that folder holds anything. " +
			"It writes whether or not the workspace indexes a file; one it indexes reaches the index as any new file does. " +
			"outcome is created, kept, updated, skipped or refused (with error)."},
		func(ctx context.Context, p workspaceScaffold) ([]ScaffoldResult, error) {
			if s.setters.scaffold == nil {
				return nil, errFixed
			}
			out, err := s.setters.scaffold.Scaffold(ctx, p.Workspace, p.Files)
			if out == nil {
				out = []ScaffoldResult{}
			}
			return out, err
		})
}
