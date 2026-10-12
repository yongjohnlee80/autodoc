// Package paths is the path helpers AutoDoc's core packages share, so each is written once. Paths
// are slash-separated and clean: a workspace's relative paths, or absolute ones.
//
// Concurrency: the package is functions of their arguments (ExpandHome also reads the home
// directory from the environment); any number of goroutines may call them.
package paths
