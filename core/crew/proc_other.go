//go:build !linux

package crew

import "errors"

// Elsewhere there is no /proc to read: every holder's state is unknown, and its lease decides.
var procs procReader = otherProcs{}

type otherProcs struct{}

func (otherProcs) dead(procRef) bool { return false }

func (otherProcs) ref(int) (procRef, error) {
	return procRef{}, errors.New("crew: process references need /proc")
}

// ancestorNamed can't tell without /proc.
func ancestorNamed(string) (int, bool) { return 0, false }
