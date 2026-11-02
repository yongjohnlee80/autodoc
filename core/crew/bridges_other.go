//go:build !linux

package crew

import "time"

// Off Linux the daemon can't read the machine's processes yet: crew.bridges answers ErrNoBridges,
// and nothing is reclaimed.

func scanHelpers(int) ([]helperProc, error) { return nil, ErrNoBridges }

type otherHelper struct{}

func openHelper(int, string) (*otherHelper, helperProc, bool, error) {
	return nil, helperProc{}, false, ErrNoBridges
}

func (*otherHelper) close() {}

func (*otherHelper) end(time.Duration) (string, error) { return "", ErrNoBridges }

func readHelperContext(string) (Context, bool) { return Context{}, false }
