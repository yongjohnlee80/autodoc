//go:build !race

package relations

// raceFactor is how much longer a wait on CPU-bound Go work may take: 1 without -race
// (race_on_test.go).
const raceFactor = 1
