package tui

import (
	"github.com/yongjohnlee80/golib/errs"
)

// ErrPanelViewAccess means a feature attempted an operation outside its
// contributed panel ids/views. Navigation actions are a separate boundary.
var ErrPanelViewAccess = errs.Sentinel(errs.ErrInvalidArgument, "view is not owned by this panel feature")

// PanelBound receives validated contributor-owned services before views are
// built. A feature may project state earlier, but may not operate views yet.
type PanelBound interface{ BindPanelHost(PanelHost) error }

type panelContribution struct {
	feature Feature
	specs   []PanelSpec
}
