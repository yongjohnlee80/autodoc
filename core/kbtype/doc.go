// Package kbtype reads a KB type and plans the files it creates
// (docs/decisions/contract.md#kb-types-are-git-repos-of-files). A KB type is a
// git repo with a kbtype.toml, which says what the type is, lists its folders with their purposes
// and proposes the workspace's settings, and a tree/, which is the KB as it is created. Read parses
// and validates a staged clone of it; Type.Plan renders the files a KB gets from it, AutoDoc's own
// KB_OPERATIONS.md included. Nothing in a type runs: its text files are read and copied, nothing
// else.
//
// The rules Read enforces are the ADR's (§4.1). The folder list is the tree: every folder listed
// has its descriptor (ABOUT.md, or _ABOUT.md in a folder whose name starts with "_"), and every
// folder under tree/ is listed; the root has AGENTS.md and ABOUT.md. tree/ never holds
// KB_OPERATIONS.md, which is AutoDoc's. An immutable folder holds only its descriptor. Every
// entry is a regular file or a folder, with a clean relative name; every file is text, at most
// MaxFileSize, and the tree at most MaxTreeSize and MaxFiles files, after a walk of at most
// MaxEntries entries. kbtype.toml is read through a MaxManifestSize limit, one byte past which is
// refused, never a truncated prefix read as valid.
//
// DeclaredVersion and Older are the managed file's version rule, scaffold.lua's declared_version
// and semver_cmp ported: the daemon replaces a KB's KB_OPERATIONS.md only when its declared
// version parses and is older than the build's.
//
// Concurrency: Read reads only the directory it is given; Plan, DeclaredVersion and Older are
// functions of their arguments. A Type is not changed after Read returns it, so any number of
// goroutines may call Plan on one.
package kbtype
