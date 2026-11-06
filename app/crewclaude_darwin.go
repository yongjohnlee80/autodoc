//go:build darwin

package app

import "github.com/yongjohnlee80/autodoc/core/crew"

// parentOf is pid's parent, from the kernel's process table (macOS has no /proc).
func parentOf(pid int) (int, error) { return crew.ParentOf(pid) }
