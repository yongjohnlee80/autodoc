// Command scratchargv (scratch, not for merge) prints its arguments as JSON.
package main

import (
	"encoding/json"
	"os"
)

func main() { _ = json.NewEncoder(os.Stdout).Encode(os.Args[1:]) }
