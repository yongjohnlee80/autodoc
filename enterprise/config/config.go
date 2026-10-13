package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
)

// The defaults of the fields that have one.
const (
	DefaultSchema   = "autodoc_search"
	DefaultListen   = "127.0.0.1:8730"
	DefaultSlots    = 4
	DefaultRefresh  = 24 * time.Hour
	DefaultInterval = time.Minute
	DefaultLag      = time.Minute
	DefaultOpenAI   = "https://api.openai.com/v1"
	DefaultOllama   = "http://127.0.0.1:11434"
	// DefaultMetricsInterval is how often the server logs its metrics lines.
	DefaultMetricsInterval = time.Minute
)

// Bounds a field is refused outside of.
const (
	MaxSlots    = 64
	MinInterval = time.Second
	// MinRefreshRetry is the soonest a failed refresh may be tried again.
	MinRefreshRetry = time.Minute
	// MinMetricsInterval is the most often the metrics lines may be logged.
	MinMetricsInterval = 10 * time.Second
	// MinTokenBytes is the shortest bearer token --server --check accepts without a warning: 32
	// random bytes, as `openssl rand -hex 32` makes. A token's hash protects where it is stored,
	// not a token short enough to guess.
	MinTokenBytes = 32
)

// Server is a validated server.toml.
type Server struct {
	Postgres  Postgres
	Embedding Embedding
	// HTTP is the HTTP API; nil when the file has no [http] table: the server then answers no
	// request, and a web application reaches the index through the Go library alone.
	HTTP    *HTTP
	Sources []Source
	Log     Log
	// Warnings are what is allowed but unwise, such as a short bearer token: --server --check
	// prints them.
	Warnings []string
}

// Postgres is where the server keeps its index: a schema of its own in the business database.
type Postgres struct {
	// DSNEnv names the environment variable holding the DSN. Load checked it is set; DSN reads it.
	DSNEnv string
	// Schema is the schema pgstore's tables live in, created when missing.
	Schema string
}

// DSN is the connection string, read from the environment when called. It is a secret: never log
// it, and never put it in an error.
func (p Postgres) DSN() string { return os.Getenv(p.DSNEnv) }

// Embedding is the enterprise's embedding provider, which embeds every tenant's chunks centrally.
type Embedding struct {
	Provider string // "openai" or "ollama"
	BaseURL  string
	Model    string
	// KeyEnv names the environment variable holding the provider's API key; "" for none.
	KeyEnv string
	// Slots is how many embedding requests may be in flight at once, across every tenant.
	Slots int
}

// Key is the provider's API key, read from the environment when called; "" when KeyEnv names none.
// It is a secret: never log it, and never put it in an error.
func (e Embedding) Key() string {
	if e.KeyEnv == "" {
		return ""
	}
	return os.Getenv(e.KeyEnv)
}

// HTTP is the HTTP API: where it listens and who may call it.
type HTTP struct {
	Listen          string
	TLSCert, TLSKey string // both or neither; required off loopback, unless Proxy
	// Proxy is the operator's word that a proxy in front terminates TLS: the API may listen off
	// loopback without TLS. It contradicts TLSCert.
	Proxy     bool
	Clients   []Client
	Operators []Operator
}

// Client is one caller of the HTTP API and what it may search.
type Client struct {
	Name string
	// TokenSHA256 is the lowercase hex SHA-256 of the client's bearer token, or "" when the
	// token is read from TokenEnv. Exactly one of the two is set.
	TokenSHA256 string
	TokenEnv    string
	// Grants are, by tenant, the values of each of the tenant's access fields the client may ask
	// for: ["*"] for any value, which a request must still name.
	Grants map[string]map[string][]string
}

// Log is what the server logs beyond its events.
type Log struct {
	// MetricsInterval is how often the metrics lines are logged; 0 logs none.
	MetricsInterval time.Duration
}

// Operator is one reader of the API's status (/v1/status), for the tenants it names: a role of
// its own, apart from the search clients, whose grants are per access value and so cannot
// authorise what status shows tenant-wide.
type Operator struct {
	Name string
	// TokenSHA256 or TokenEnv, exactly one, as a Client's.
	TokenSHA256 string
	TokenEnv    string
	// Tenants are the tenants whose status it may read: configured ones, or ["*"] for every one.
	Tenants []string
}

// Source is a .view file indexed into a tenant of its own.
type Source struct {
	Tenant string
	// View is the .view file's path, resolved against the configuration file's directory.
	View string
	// Refresh is how often every row is read again, and documents of rows gone are deleted.
	Refresh time.Duration
	// Interval is how often the rows changed since the cursor are read.
	Interval time.Duration
	// Lag is how far behind the database's clock a pass reads, so a business transaction still
	// open cannot commit a row behind the cursor.
	Lag time.Duration
	// RefreshRetry is how soon a refresh that failed or did not complete is tried again; 0 is the
	// ingester's default.
	RefreshRetry time.Duration
	// Access are the row fields every search of the tenant must be scoped by.
	Access []string
}

// Error is one refusal: the field it is about and why.
type Error struct {
	Field  string // as the file spells it: postgres.dsn_env, source[1].interval, http.client[0].grants.lm-catalog
	Reason string
}

func (e *Error) Error() string { return e.Field + ": " + e.Reason }

// Refusal is every refusal of one file.
type Refusal struct {
	Path   string
	Errors []*Error
}

func (r *Refusal) Error() string {
	lines := make([]string, len(r.Errors))
	for i, e := range r.Errors {
		lines[i] = e.Error()
	}
	return r.Path + ": " + strings.Join(lines, "; ")
}

// Fields are the refused fields, in the order they were found.
func (r *Refusal) Fields() []string {
	out := make([]string, len(r.Errors))
	for i, e := range r.Errors {
		out[i] = e.Field
	}
	return out
}

// Refuse is a Refusal of one field: for a check made after Load, such as a view's, so that every
// refusal of the configuration reads alike.
func Refuse(path, field, reason string) *Refusal {
	return &Refusal{Path: path, Errors: []*Error{{Field: field, Reason: reason}}}
}

// file is server.toml as TOML decodes it.
type file struct {
	Postgres struct {
		DSNEnv    string `toml:"dsn_env"`
		DSNSealed string `toml:"dsn_sealed"`
		Schema    string `toml:"schema"`
	} `toml:"postgres"`
	Embedding *struct {
		Provider string `toml:"provider"`
		BaseURL  string `toml:"base_url"`
		Model    string `toml:"model"`
		KeyEnv   string `toml:"key_env"`
		Slots    *int   `toml:"slots"`
	} `toml:"embedding"`
	HTTP *struct {
		Listen  string `toml:"listen"`
		TLSCert string `toml:"tls_cert"`
		TLSKey  string `toml:"tls_key"`
		Proxy   bool   `toml:"proxy"`
		Clients []struct {
			Name        string                         `toml:"name"`
			TokenSHA256 string                         `toml:"token_sha256"`
			TokenEnv    string                         `toml:"token_env"`
			Grants      map[string]map[string][]string `toml:"grants"`
		} `toml:"client"`
		Operators []struct {
			Name        string   `toml:"name"`
			TokenSHA256 string   `toml:"token_sha256"`
			TokenEnv    string   `toml:"token_env"`
			Tenants     []string `toml:"tenants"`
		} `toml:"operator"`
	} `toml:"http"`
	Log *struct {
		MetricsInterval *duration `toml:"metrics_interval"`
	} `toml:"log"`
	Sources []struct {
		Tenant   string    `toml:"tenant"`
		View     string    `toml:"view"`
		Refresh  *duration `toml:"refresh"`
		Interval *duration `toml:"interval"`
		Lag      *duration `toml:"lag"`
		Retry    *duration `toml:"refresh_retry"`
		Access   []string  `toml:"access"`
	} `toml:"source"`
}

// duration is a time.Duration written as a string ("24h", "90s").
type duration struct{ time.Duration }

func (d *duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return fmt.Errorf("want a duration such as \"90s\" or \"24h\"")
	}
	d.Duration = v
	return nil
}

// Load reads and validates the configuration at path. A file that cannot be read is an error; a
// file that is wrong is a *Refusal naming every field that is.
func Load(path string) (*Server, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f file
	md, err := toml.Decode(string(src), &f)
	if err != nil {
		// a decode error names the line and key, never a value; the field is the file itself
		return nil, Refuse(path, "(file)", tomlReason(err))
	}
	v := &validator{path: path, dir: filepath.Dir(path)}
	for _, k := range md.Undecoded() {
		v.refuse(k.String(), "unknown field")
	}
	s := &Server{}
	s.Postgres = v.postgres(f)
	s.Sources = v.sources(f)
	s.Embedding = v.embedding(f)
	s.HTTP = v.http(f, s.Sources)
	s.Log = v.log(f)
	s.Warnings = v.warnings
	if len(v.errs) > 0 {
		return nil, &Refusal{Path: path, Errors: v.errs}
	}
	return s, nil
}

// tomlReason is a decode error as a reason: its first line, which names where it is.
func tomlReason(err error) string {
	msg := err.Error()
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	return strings.TrimPrefix(msg, "toml: ")
}

type validator struct {
	path, dir string
	errs      []*Error
	warnings  []string
}

func (v *validator) refuse(field, format string, args ...any) {
	v.errs = append(v.errs, &Error{Field: field, Reason: fmt.Sprintf(format, args...)})
}

// envSet checks that field names an environment variable that is set and not empty. The value is
// never read into a message.
func (v *validator) envSet(field, name string) bool {
	if !envName.MatchString(name) {
		v.refuse(field, "%q is not an environment variable name", name)
		return false
	}
	if os.Getenv(name) == "" {
		v.refuse(field, "the environment variable %s is not set", name)
		return false
	}
	return true
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (v *validator) postgres(f file) Postgres {
	p := Postgres{DSNEnv: f.Postgres.DSNEnv, Schema: f.Postgres.Schema}
	switch {
	case f.Postgres.DSNSealed != "":
		// NOTE: AutoDoc's keyslot seals secrets with a key kept beside a local store, which the
		// server does not have; a sealed DSN needs a key of the server's own, and a way to seal
		// one, first.
		v.refuse("postgres.dsn_sealed", "a sealed DSN is not supported yet: set dsn_env to the name of an environment variable holding the DSN")
	case p.DSNEnv == "":
		v.refuse("postgres.dsn_env", "required: the name of the environment variable holding the DSN")
	default:
		v.envSet("postgres.dsn_env", p.DSNEnv)
	}
	if p.Schema == "" {
		p.Schema = DefaultSchema
	}
	if err := pgstore.CheckSchema(p.Schema); err != nil {
		v.refuse("postgres.schema", "%q is not a plain schema name: lowercase letters, digits and _, not starting with a digit or pg_, at most 63", p.Schema)
	}
	return p
}

func (v *validator) embedding(f file) Embedding {
	if f.Embedding == nil {
		v.refuse("embedding", "required: the provider every tenant's chunks are embedded with")
		return Embedding{}
	}
	e := Embedding{Provider: f.Embedding.Provider, BaseURL: f.Embedding.BaseURL, Model: f.Embedding.Model,
		KeyEnv: f.Embedding.KeyEnv, Slots: DefaultSlots}
	switch e.Provider {
	case "openai":
		if e.BaseURL == "" {
			e.BaseURL = DefaultOpenAI
		}
	case "ollama":
		if e.BaseURL == "" {
			e.BaseURL = DefaultOllama
		}
	case "":
		v.refuse("embedding.provider", `required: "openai" or "ollama"`)
	default:
		v.refuse("embedding.provider", `%q: want "openai" or "ollama"`, e.Provider)
	}
	if e.BaseURL != "" {
		if u, err := url.Parse(e.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			v.refuse("embedding.base_url", "want an http or https URL with a host")
		}
	}
	if strings.TrimSpace(e.Model) == "" {
		v.refuse("embedding.model", "required: the model every tenant's chunks are embedded with")
	}
	if e.KeyEnv != "" {
		v.envSet("embedding.key_env", e.KeyEnv)
	}
	if f.Embedding.Slots != nil {
		e.Slots = *f.Embedding.Slots
		if e.Slots < 1 || e.Slots > MaxSlots {
			v.refuse("embedding.slots", "%d: want 1 to %d", e.Slots, MaxSlots)
		}
	}
	return e
}

// tenantName is a tenant as a source names it: it is a key in every pgstore table, a log label and
// part of the writer lock's key.
var tenantName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func (v *validator) sources(f file) []Source {
	if len(f.Sources) == 0 {
		v.refuse("source", "at least one [[source]] is required")
	}
	seen := map[string]int{}
	out := make([]Source, 0, len(f.Sources))
	for i, raw := range f.Sources {
		at := func(k string) string { return fmt.Sprintf("source[%d].%s", i, k) }
		s := Source{Tenant: raw.Tenant, Refresh: DefaultRefresh, Interval: DefaultInterval, Lag: DefaultLag,
			Access: raw.Access}
		switch {
		case s.Tenant == "":
			v.refuse(at("tenant"), "required")
		case !tenantName.MatchString(s.Tenant):
			v.refuse(at("tenant"), "%q: letters, digits, '.', '_' and '-', starting with a letter or digit, at most 64", s.Tenant)
		default:
			if j, dup := seen[s.Tenant]; dup {
				v.refuse(at("tenant"), "%q is source[%d]'s tenant too: each source is a tenant of its own", s.Tenant, j)
			}
			seen[s.Tenant] = i
		}
		if raw.View == "" {
			v.refuse(at("view"), "required: the .view file the tenant is indexed from")
		} else {
			s.View = raw.View
			if !filepath.IsAbs(s.View) {
				s.View = filepath.Join(v.dir, s.View)
			}
		}
		if raw.Interval != nil {
			s.Interval = raw.Interval.Duration
		}
		if raw.Refresh != nil {
			s.Refresh = raw.Refresh.Duration
		}
		if raw.Lag != nil {
			s.Lag = raw.Lag.Duration
		}
		if s.Interval < MinInterval {
			v.refuse(at("interval"), "%v: want at least %v", s.Interval, MinInterval)
		}
		if s.Refresh < s.Interval {
			v.refuse(at("refresh"), "%v: want at least the interval (%v)", s.Refresh, s.Interval)
		}
		if s.Lag < 0 {
			v.refuse(at("lag"), "%v: want zero or more", s.Lag)
		}
		if raw.Retry != nil {
			s.RefreshRetry = raw.Retry.Duration
			if s.RefreshRetry < MinRefreshRetry || s.RefreshRetry > s.Refresh {
				v.refuse(at("refresh_retry"), "%v: want %v up to the refresh (%v)", s.RefreshRetry, MinRefreshRetry, s.Refresh)
			}
		}
		if len(s.Access) == 0 {
			v.refuse(at("access"), "required: the row fields every search of the tenant is scoped by")
		} else if err := pgstore.CheckAccessFields(s.Access); err != nil {
			v.refuse(at("access"), "%s", strings.TrimPrefix(err.Error(), "pgstore: "))
		}
		out = append(out, s)
	}
	return out
}

func (v *validator) http(f file, sources []Source) *HTTP {
	if f.HTTP == nil {
		return nil
	}
	h := &HTTP{Listen: f.HTTP.Listen, TLSCert: f.HTTP.TLSCert, TLSKey: f.HTTP.TLSKey, Proxy: f.HTTP.Proxy}
	if h.Listen == "" {
		h.Listen = DefaultListen
	}
	host, _, err := net.SplitHostPort(h.Listen)
	if err != nil {
		v.refuse("http.listen", "%q: want host:port", h.Listen)
	}
	switch {
	case h.Proxy && h.TLSCert != "":
		v.refuse("http.proxy", "a proxy terminating TLS and tls_cert contradict: give one or the other")
	case (h.TLSCert == "") != (h.TLSKey == ""):
		v.refuse("http.tls_cert", "tls_cert and tls_key go together: give both or neither")
	case h.TLSCert != "":
		for _, p := range []struct {
			field string
			path  *string
		}{{"http.tls_cert", &h.TLSCert}, {"http.tls_key", &h.TLSKey}} {
			if !filepath.IsAbs(*p.path) {
				*p.path = filepath.Join(v.dir, *p.path)
			}
			if _, err := os.Stat(*p.path); err != nil {
				v.refuse(p.field, "%s cannot be read", *p.path)
			}
		}
	case err == nil && !loopback(host) && !h.Proxy:
		v.refuse("http.listen", "%q is not loopback: listening beyond this host needs tls_cert and tls_key, or proxy = true behind a proxy that terminates TLS", h.Listen)
	case err == nil && !loopback(host):
		v.warnings = append(v.warnings, fmt.Sprintf("http.proxy: %s serves plain HTTP off loopback: every request crosses the network from your proxy in clear text, so keep that network private", h.Listen))
	}
	access := map[string][]string{}
	for _, s := range sources {
		access[s.Tenant] = s.Access
	}
	if len(f.HTTP.Clients) == 0 {
		v.refuse("http.client", "at least one [[http.client]] is required: the API answers no one else")
	}
	names := map[string]string{}  // a name → the client or operator that has it
	tokens := map[string]string{} // a token's identity → the client that holds it
	for i, raw := range f.HTTP.Clients {
		at := func(k string) string { return fmt.Sprintf("http.client[%d].%s", i, k) }
		c := Client{Name: raw.Name, TokenSHA256: strings.ToLower(raw.TokenSHA256), TokenEnv: raw.TokenEnv, Grants: raw.Grants}
		if c.Name != "" {
			at = func(k string) string { return fmt.Sprintf("http.client[%d] (%s).%s", i, c.Name, k) }
		}
		switch {
		case c.Name == "":
			v.refuse(at("name"), "required: it names the client in the log")
		default:
			if other, dup := names[c.Name]; dup {
				v.refuse(at("name"), "%s has this name too: names are unique", other)
			}
			names[c.Name] = fmt.Sprintf("http.client[%d]", i)
		}
		switch {
		case c.TokenSHA256 != "" && c.TokenEnv != "":
			v.refuse(at("token_sha256"), "give token_sha256 or token_env, not both")
		case c.TokenSHA256 == "" && c.TokenEnv == "":
			v.refuse(at("token_sha256"), "required: token_sha256 (the token's SHA-256) or token_env (a variable holding the token)")
		case c.TokenSHA256 != "":
			// the value is never echoed: a token pasted here by mistake must not reach a log
			if b, err := hex.DecodeString(c.TokenSHA256); err != nil || len(b) != 32 {
				v.refuse(at("token_sha256"), "want the 64 hex digits of the token's SHA-256, never the token itself")
			}
		default:
			if v.envSet(at("token_env"), c.TokenEnv) && len(os.Getenv(c.TokenEnv)) < MinTokenBytes {
				v.warnings = append(v.warnings, fmt.Sprintf("%s: the token in %s is shorter than %d bytes: use a random token of at least 32 bytes (openssl rand -hex 32)",
					at("token_env"), c.TokenEnv, MinTokenBytes))
			}
		}
		if id := tokenIdentity(c.TokenSHA256, c.TokenEnv); id != "" {
			tokens[id] = fmt.Sprintf("http.client[%d]", i)
		}
		if len(c.Grants) == 0 {
			v.refuse(at("grants"), "required: the tenants the client may search, and per tenant the values of each access field")
		}
		for _, tenant := range sortedKeys(c.Grants) {
			fields := c.Grants[tenant]
			g := at("grants." + tenant)
			want, ok := access[tenant]
			if !ok {
				v.refuse(g, "%q is not a configured source's tenant", tenant)
				continue
			}
			for _, field := range want {
				if _, ok := fields[field]; !ok {
					v.refuse(g+"."+field, "required: every access field of a granted tenant needs a grant")
				}
			}
			for _, field := range sortedKeys(fields) {
				if !slices.Contains(want, field) {
					v.refuse(g+"."+field, "%q is not one of the tenant's access fields %v", field, want)
					continue
				}
				v.grant(g+"."+field, fields[field])
			}
		}
		h.Clients = append(h.Clients, c)
	}
	for i, raw := range f.HTTP.Operators {
		at := func(k string) string { return fmt.Sprintf("http.operator[%d].%s", i, k) }
		o := Operator{Name: raw.Name, TokenSHA256: strings.ToLower(raw.TokenSHA256), TokenEnv: raw.TokenEnv, Tenants: raw.Tenants}
		if o.Name != "" {
			at = func(k string) string { return fmt.Sprintf("http.operator[%d] (%s).%s", i, o.Name, k) }
		}
		switch {
		case o.Name == "":
			v.refuse(at("name"), "required: it names the operator in the log")
		default:
			if other, dup := names[o.Name]; dup {
				v.refuse(at("name"), "%s has this name too: names are unique across clients and operators", other)
			}
			names[o.Name] = fmt.Sprintf("http.operator[%d]", i)
		}
		v.token(at, o.TokenSHA256, o.TokenEnv)
		if id := tokenIdentity(o.TokenSHA256, o.TokenEnv); id != "" {
			if holder, both := tokens[id]; both {
				// never the token: only who holds it twice
				v.refuse(at("token_sha256"), "%s holds the same token: one token, one role", holder)
			}
			tokens[id] = fmt.Sprintf("http.operator[%d]", i)
		}
		v.tenants(at("tenants"), o.Tenants, access)
		h.Operators = append(h.Operators, o)
	}
	return h
}

// token checks a token given one way or the other, as a client's is.
func (v *validator) token(at func(string) string, sha, env string) {
	switch {
	case sha != "" && env != "":
		v.refuse(at("token_sha256"), "give token_sha256 or token_env, not both")
	case sha == "" && env == "":
		v.refuse(at("token_sha256"), "required: token_sha256 (the token's SHA-256) or token_env (a variable holding the token)")
	case sha != "":
		if b, err := hex.DecodeString(sha); err != nil || len(b) != 32 {
			v.refuse(at("token_sha256"), "want the 64 hex digits of the token's SHA-256, never the token itself")
		}
	default:
		if v.envSet(at("token_env"), env) && len(os.Getenv(env)) < MinTokenBytes {
			v.warnings = append(v.warnings, fmt.Sprintf("%s: the token in %s is shorter than %d bytes: use a random token of at least 32 bytes (openssl rand -hex 32)",
				at("token_env"), env, MinTokenBytes))
		}
	}
}

// tokenIdentity is a token as the API compares it, its SHA-256 in hex, from either form; "" when
// neither gives one. Two roles holding one token have the same identity.
func tokenIdentity(sha, env string) string {
	switch {
	case sha != "":
		return sha
	case env != "" && os.Getenv(env) != "":
		sum := sha256.Sum256([]byte(os.Getenv(env)))
		return hex.EncodeToString(sum[:])
	}
	return ""
}

// tenants checks an operator's tenants: configured ones, each once, or ["*"] alone.
func (v *validator) tenants(field string, tenants []string, access map[string][]string) {
	if len(tenants) == 0 {
		v.refuse(field, `required: the tenants whose status the operator reads, or ["*"]`)
		return
	}
	if slices.Contains(tenants, "*") {
		if len(tenants) > 1 {
			v.refuse(field, `"*" stands alone: it already names every tenant`)
		}
		return
	}
	seen := map[string]bool{}
	for _, t := range tenants {
		if _, ok := access[t]; !ok {
			v.refuse(field, "%q is not a configured source's tenant", t)
		}
		if seen[t] {
			v.refuse(field, "%q is named twice", t)
		}
		seen[t] = true
	}
}

// log reads [log]: the metrics lines' interval, at least MinMetricsInterval, or 0 for none.
func (v *validator) log(f file) Log {
	l := Log{MetricsInterval: DefaultMetricsInterval}
	if f.Log != nil && f.Log.MetricsInterval != nil {
		l.MetricsInterval = f.Log.MetricsInterval.Duration
		if l.MetricsInterval != 0 && l.MetricsInterval < MinMetricsInterval {
			v.refuse("log.metrics_interval", "%v: want 0 (none) or at least %v", l.MetricsInterval, MinMetricsInterval)
		}
	}
	return l
}

// grant checks one field's grant: ["*"] alone, or distinct, non-empty values.
func (v *validator) grant(field string, values []string) {
	if len(values) == 0 {
		v.refuse(field, `empty: give ["*"] for any value, or the values allowed`)
		return
	}
	seen := map[string]bool{}
	for _, val := range values {
		switch {
		case val == "*" && len(values) > 1:
			v.refuse(field, `"*" stands alone: it already allows every value`)
			return
		case strings.TrimSpace(val) == "":
			v.refuse(field, "an empty value")
			return
		case seen[val]:
			v.refuse(field, "%q is granted twice", val)
			return
		}
		seen[val] = true
	}
}

// loopback reports whether host is a loopback address or localhost.
func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// IsRefusal reports whether err is a configuration refusal: the server's exit code 2.
func IsRefusal(err error) bool {
	var r *Refusal
	return errors.As(err, &r)
}
