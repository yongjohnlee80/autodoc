package agents

import (
	"encoding/json"
	"os"
	"testing"
)

// TestMain is an agent CLI's stand-in when a cell runs this test binary with argvPrinterEnv set:
// it prints its arguments as JSON and exits (printingCLI), as the host's test binary does for the
// host's cells.
func TestMain(m *testing.M) {
	if os.Getenv(argvPrinterEnv) == "1" {
		_ = json.NewEncoder(os.Stdout).Encode(os.Args[1:])
		os.Exit(0)
	}
	os.Exit(m.Run())
}
