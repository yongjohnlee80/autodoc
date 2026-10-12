// Package workspace opens a workspace's root: its files as a vfs.FS and the Matcher of its include
// and exclude patterns. The workspace itself is a row of the store (core/store), and the store's
// lease makes the daemon the only one serving it.
//
// A Spec is a workspace as it is added; Normalize checks it and fills the default patterns. A
// pattern is a root-relative glob: segments split on '/', each a path.Match pattern or "**" for
// any number of whole segments, with bounded, non-nested brace alternatives ("*.{md,txt}"). A file
// is in the workspace when it matches an include pattern and no exclude pattern.
//
// Concurrency: ExpandPattern, ValidPattern, DefaultInclude, DefaultExclude and Normalize are
// functions of their arguments (Normalize also reads the home directory from the environment to
// expand "~/"); any goroutine may call them. Open reads the disk. A Matcher is not changed after
// NewMatcher, so Match and Excluded are safe for concurrent use. The FS of a Workspace that Open
// returns is golib's local driver: safe for concurrent use, and Close may be called more than
// once, after which every FS call fails. The package does not write a Workspace's fields after
// Open; a caller that writes them must serialize that with every reader.
package workspace
