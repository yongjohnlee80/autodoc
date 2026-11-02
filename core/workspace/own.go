package workspace

// OwnDir is one of AutoDoc's own folders: Name is "config", "state" or "data".
type OwnDir struct{ Name, Path string }

// Overlap is a root's overlap with one of AutoDoc's own folders.
type Overlap struct{ Dir, Relation, Path string }

// Overlaps is root's overlaps with own.
func Overlaps(root string, own []OwnDir) []Overlap { return nil }

// CanonicalDirs is own, each path where it leads.
func CanonicalDirs(own []OwnDir) []OwnDir { return own }
