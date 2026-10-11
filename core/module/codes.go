package module

import "fmt"

// Code ranges, one per module, so two modules cannot answer the same code. A module's public error
// outside its range is refused when it is registered (CheckCodes). The protocol's own codes
// (-32020, -32021) and the daemon's shared ones (-32060 to -32070) belong to rpc.
var codeRanges = map[string][2]int64{
	"todos": {-32089, -32071},
}

// Shared reports whether code is one of the daemon's shared codes (-32060 to -32070: no such
// workspace, not found, conflict …), which any module may answer for the same condition, with the
// meaning rpc gives it.
func Shared(code int64) bool { return code >= -32070 && code <= -32060 }

// CheckCodes reports a public error of m whose code is neither shared nor in m's range, or any
// unshared code when m has no range: the host refuses such a module at start.
func CheckCodes(m Module) error {
	r, ok := codeRanges[m.Name()]
	for _, e := range m.Errors() {
		if Shared(e.Code) {
			continue
		}
		if !ok {
			return fmt.Errorf("module %s: has no code range for %s (%d)", m.Name(), e.Name, e.Code)
		}
		if e.Code < r[0] || e.Code > r[1] {
			return fmt.Errorf("module %s: %s's code %d is outside %d…%d", m.Name(), e.Name, e.Code, r[0], r[1])
		}
	}
	return nil
}
