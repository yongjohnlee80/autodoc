//go:build race

package relations

// raceFactor is how much longer a wait on CPU-bound Go work may take in a -race build.
const raceFactor = 4
