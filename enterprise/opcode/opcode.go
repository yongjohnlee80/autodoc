// Package opcode is the closed set of codes enterprise search reports its failures by to an
// operator (GET /v1/status, the metrics lines): a code, never an error's text, which may hold what
// the operator should not see and stays in the log, redacted.
package opcode

// Code is one failure's kind; None is no failure.
type Code string

// The codes. A failure no other code names is Other.
const (
	None                Code = ""
	Database            Code = "database"             // the index's database
	LockLost            Code = "lock_lost"            // the tenant's writer lock was lost
	View                Code = "view"                 // a view's query failed on the business database
	Order               Code = "order"                // a view's rows not in (updated_at, id) order, or a page that holds the cursor
	ProviderUnreachable Code = "provider_unreachable" // the embedding provider did not answer
	ProviderRefused     Code = "provider_refused"     // the provider refused the key or the model
	ProviderRejected    Code = "provider_rejected"    // the provider rejected texts
	Other               Code = "other"
)
