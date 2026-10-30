package crew

import "github.com/yongjohnlee80/golib/errs"

// The crew.* verbs' public codes, in the module's range (core/module codes.go), ADR 1793271500 §4.1.
const (
	CodeScopeBusy       int64 = -32090 // another live owner holds the scope: wait, or crew.takeover after the user agrees
	CodeStaleEpoch      int64 = -32091 // the scope changed hands since the epoch was issued: claim it again
	CodeCodenameBusy    int64 = -32092 // the profile already has a live session: offer the user take-over
	CodeInstanceReused  int64 = -32093 // a registration replay with another secret, codename, kind or workspace
	CodeBadCredential   int64 = -32094 // no such live session for the credential
	CodeContextMismatch int64 = -32095 // two session contexts disagree, or one is for another store
	CodeBadEndpoint     int64 = -32096 // an endpoint whose kind or locator the daemon refuses
)

var (
	// ErrScopeBusy is a claim of a scope another live owner holds (or one adoption has ended).
	ErrScopeBusy = errs.Sentinel(errs.ErrPrecondition, "crew: the scope is held")

	// ErrStaleEpoch is a host verb from an owner a claim, takeover or adoption has since fenced.
	ErrStaleEpoch = errs.Sentinel(errs.ErrPrecondition, "crew: stale epoch")

	// ErrCodenameBusy is a registration of a profile that already has a live session.
	ErrCodenameBusy = errs.Sentinel(errs.ErrPrecondition, "crew: the codename has a live session")

	// ErrInstanceReused is a registration replay that does not match the session it names.
	ErrInstanceReused = errs.Sentinel(errs.ErrPrecondition, "crew: the instance was registered differently")

	// ErrBadCredential is a credential naming no live session, or with the wrong secret.
	ErrBadCredential = errs.Sentinel(errs.ErrInvalidArgument, "crew: bad credential")

	// ErrContextMismatch is a session context that disagrees with another, or names another store.
	ErrContextMismatch = errs.Sentinel(errs.ErrInvalidArgument, "crew: session contexts disagree")

	// ErrBadEndpoint is an endpoint whose kind or locator is refused.
	ErrBadEndpoint = errs.Sentinel(errs.ErrInvalidArgument, "crew: bad endpoint")

	// ErrInvalid is a param the crew verbs refuse; the message names it. It answers as invalid
	// params.
	ErrInvalid = errs.Sentinel(errs.ErrInvalidArgument, "crew: invalid")
)
