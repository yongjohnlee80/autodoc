// Package config reads AutoDoc's configuration file and resolves the places AutoDoc keeps things:
// the unix socket the daemon listens on, the state directory, and the store.
//
// The file is TOML, at DefaultPath unless a caller names another. Every setting is optional:
//
//	[server]
//	socket = ""                 # default: $XDG_RUNTIME_DIR/autodoc.sock
//	state_dir = ""              # default: $XDG_STATE_HOME/autodoc
//	data_dir = ""               # default: $XDG_DATA_HOME/autodoc (the store: autodoc.db)
//
//	[follow]
//	poll_interval = "2s"        # the watch fallback's listing interval
//	[embedding_queue]
//	max_inflight = 1            # requests to the shared provider, 1 or 2
//
// Load fills the defaults and checks what it can without the filesystem. A missing file is every
// default. A file that does not parse, or names a setting the package does not know, is ErrInvalid,
// so a misspelled setting is reported rather than ignored. A file that cannot be read is an error
// that is not ErrInvalid.
//
// Server's path methods resolve an empty setting from the XDG variables, then the home directory,
// and create nothing. A configured state_dir or data_dir may start with "~/". A socket path is
// used as written, and one over 100 bytes is ErrInvalid, below the kernels' sockaddr_un limits.
//
// Workspaces and embedding providers are not configured here. They are rows of the store, added in
// the TUI (providers with their keys sealed, in Preferences) or with workspace.add. A file that
// still has an [[workspace]] or [embedding] section is ErrInvalid, with a message that says where
// the setting lives now.
//
// Concurrency: the package has no mutable state. Load, DefaultPath and Server's SocketPath,
// StateDirPath and StorePath may be called from any goroutine. Load reads the file; DefaultPath and
// the path methods read the environment (the XDG variables, the home directory, and $TMPDIR for
// the socket on Darwin). A Config is plain data the package does not change after Load returns:
// goroutines may read one concurrently, and a caller that writes a field must serialize that
// write with every reader.
package config
