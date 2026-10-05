//go:build race

package tui

// raceFactor is how much longer a wait on CPU-bound Go work may take in a -race build: its image
// decoding and encoding run some 25 times slower (a long page's strips, 0.5 s, take 13 s).
const raceFactor = 4
