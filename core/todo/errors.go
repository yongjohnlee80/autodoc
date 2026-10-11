package todo

import "github.com/yongjohnlee80/golib/errs"

var (
	// ErrStale is a mutation whose rev is not the file's current one: the task changed since the
	// client read it. The client reads it again with Get and retries with the new rev.
	ErrStale = errs.Sentinel(errs.ErrPrecondition, "todo: the task changed since it was read")

	// ErrNotFound is a task id, a malformed file or a consumer the store does not have.
	ErrNotFound = errs.Sentinel(errs.ErrInvalidArgument, "todo: no such task")

	// ErrInvalid is a value the format or the rules refuse; the message names the field.
	ErrInvalid = errs.Sentinel(errs.ErrInvalidArgument, "todo: invalid")

	// ErrNotAStore is a location with no todo store: Scaffold creates one, SetDir points elsewhere.
	ErrNotAStore = errs.Sentinel(errs.ErrInvalidArgument, "todo: not a todo store")

	// ErrExists is an id already taken, or a MoveStore target that exists.
	ErrExists = errs.Sentinel(errs.ErrPrecondition, "todo: already exists")

	// ErrNotWritable is a write outside the writable stores: a workspace's or registered root's
	// .todo-list, or a SetDir override.
	ErrNotWritable = errs.Sentinel(errs.ErrPrecondition, "todo: not a writable store")

	// ErrUnclaimed is a write to a store the daemon has not claimed: until the cutover, auto-core
	// writes the store and the daemon only reads it.
	ErrUnclaimed = errs.Sentinel(errs.ErrPrecondition, "todo: the store is not claimed")

	// ErrNoWorkspace is an At naming a workspace the daemon does not have.
	ErrNoWorkspace = errs.Sentinel(errs.ErrInvalidArgument, "todo: no such workspace")
)
