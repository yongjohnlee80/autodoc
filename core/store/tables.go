package store

import "github.com/yongjohnlee80/golib/dao"

// The store's tables, one row type and one declaration each. Every column is
// T(table, column), so a join never makes one ambiguous. The declarations are
// unexported: the only way to a workspace-owned table is a Scope.

// wsCol is the workspace key every workspace-owned table carries.
const wsCol = "workspace_id"

// Workspace is a root AutoDoc indexes.
type Workspace struct {
	ID                   int64
	Name, Root           string
	EmbeddingPolicy      string
	IncludeEmpty         int64
	CommitSeq, ChangeSeq int64
	SectionTokens        *int64
	SchemaPath           *string // the frontmatter schema file: absolute, or under Root; nil: none (000007)
	TextExtensions       *string // a JSON array of the workspace's own plain-text extensions; nil: none (000008)
	ProviderID           *int64  // the embedding provider the workspace uses instead of the daemon's; nil: the daemon's (000010)
	UID                  *string // 128 random bits in hex, its identity in a shared destination database (000011)
	Destination          string  // where its index lives: "sqlite", the local store, or "postgres" (000011)
	VectorIndex          *string // a Postgres destination's pgvector index method, "hnsw" or "ivfflat"; nil: the default (000011)
	ViewArgs             *string // the default arguments of its .view files, by name, as a JSON object; nil: none (000011)
	AbstractChunk        int64   // 1: each document's abstract is a chunk of its own (000014)
	DemoteSuperseded     int64   // 1: a superseded document's hits sit below its successor's (000014)
	GraphOnlySet         int64   // 1: its graph_only patterns were set; 0: it has the default (000021)
	TodoSearch           int64   // 1: its todo tasks are indexed in full, for search (000021)
	CreatedAt, UpdatedAt int64
}

// WorkspaceField names a workspace column.
type WorkspaceField string

const (
	WorkspaceID              WorkspaceField = "id"
	WorkspaceName            WorkspaceField = "name"
	WorkspaceRoot            WorkspaceField = "root"
	WorkspaceCommitSeq       WorkspaceField = "commit_seq"
	WorkspaceChangeSeq       WorkspaceField = "change_seq"
	WorkspaceSectionTokens   WorkspaceField = "section_tokens"
	WorkspaceEmbeddingPolicy WorkspaceField = "embedding_policy"
	WorkspaceIncludeEmpty    WorkspaceField = "include_empty"
	WorkspaceSchemaPath      WorkspaceField = "schema_path"
	WorkspaceTextExtensions  WorkspaceField = "text_extensions"
	WorkspaceProviderID      WorkspaceField = "provider_id"
	WorkspaceUID             WorkspaceField = "uid"
	WorkspaceDestination     WorkspaceField = "destination"
	WorkspaceVectorIndex     WorkspaceField = "vector_index"
	WorkspaceViewArgs        WorkspaceField = "view_args"
	WorkspaceAbstractChunk   WorkspaceField = "abstract_chunk"
	WorkspaceDemote          WorkspaceField = "demote_superseded"
	WorkspaceGraphOnlySet    WorkspaceField = "graph_only_set"
	WorkspaceTodoSearch      WorkspaceField = "todo_search"
	WorkspaceCreatedAt       WorkspaceField = "created_at"
	WorkspaceUpdatedAt       WorkspaceField = "updated_at"
)

// Connection is one of a workspace's databases: its source, where the .view files run, or its
// destination, where its index lives. DSN is sealed by the keyslot; Connection and SetConnection
// open and seal it (connections.go).
type Connection struct {
	WorkspaceID int64
	Role        string // "source" or "destination"
	Engine      string // "postgres" or "sqlite"
	DSN         []byte
	Schema      *string // a Postgres schema; nil: the connection's default
	UpdatedAt   int64
}

// ConnectionField names a workspace_connection column.
type ConnectionField string

const (
	ConnWorkspace ConnectionField = "workspace_id"
	ConnRole      ConnectionField = "role"
	ConnEngine    ConnectionField = "engine"
	ConnDSN       ConnectionField = "dsn"
	ConnSchema    ConnectionField = "schema_name"
	ConnUpdatedAt ConnectionField = "updated_at"
)

// Preference is one of a client's preferences, by name: the store's, not a
// workspace's.
type Preference struct {
	Name, Value string
	UpdatedAt   int64
}

// PreferenceField names a preference column.
type PreferenceField string

const (
	PrefName      PreferenceField = "name"
	PrefValue     PreferenceField = "value"
	PrefUpdatedAt PreferenceField = "updated_at"
)

// Provider is an embedding provider. APIKey is the key as the store keeps it, sealed.
type Provider struct {
	ID                   int64
	Name, Kind           string
	BaseURL, Model       string
	Context              int64 // the context window, in tokens (000003)
	APIKey               []byte
	CreatedAt, UpdatedAt int64
}

// ProviderField names an embedding_provider column.
type ProviderField string

const (
	ProviderID        ProviderField = "id"
	ProviderName      ProviderField = "name"
	ProviderKind      ProviderField = "kind"
	ProviderBaseURL   ProviderField = "base_url"
	ProviderModel     ProviderField = "model"
	ProviderContext   ProviderField = "context"
	ProviderAPIKey    ProviderField = "api_key"
	ProviderCreatedAt ProviderField = "created_at"
	ProviderUpdatedAt ProviderField = "updated_at"
)

// Ranker is a ranker model (000013). APIKey is the key as the store keeps it, sealed.
type Ranker struct {
	ID                   int64
	Name, Kind           string
	BaseURL, Model       string
	APIKey               []byte
	CreatedAt, UpdatedAt int64
}

// RankerField names a ranker column.
type RankerField string

const (
	RankerID        RankerField = "id"
	RankerName      RankerField = "name"
	RankerKind      RankerField = "kind"
	RankerBaseURL   RankerField = "base_url"
	RankerModel     RankerField = "model"
	RankerAPIKey    RankerField = "api_key"
	RankerCreatedAt RankerField = "created_at"
	RankerUpdatedAt RankerField = "updated_at"
)

// Usage is a provider's or a ranker's use on one day (UTC, YYYY-MM-DD); ProviderID is the ranker's
// id in ranker_usage.
type Usage struct {
	ProviderID                                 int64
	Day                                        string
	Requests, Texts, Tokens, Failures, Limited int64
}

// UsageField names an embedding_usage column.
type UsageField string

const (
	UsageProvider UsageField = "provider_id"
	UsageDay      UsageField = "day"
	UsageRequests UsageField = "requests"
	UsageTexts    UsageField = "texts"
	UsageTokens   UsageField = "tokens"
	UsageFailures UsageField = "failures"
	UsageLimited  UsageField = "limited"
)

// LogEntry is one of a provider's or a ranker's recent calls; ProviderID is the ranker's id in
// ranker_log.
type LogEntry struct {
	ID, ProviderID            int64
	At, Texts, Tokens, Millis int64
	Outcome                   string
}

// LogField names an embedding_log column.
type LogField string

const (
	LogID       LogField = "id"
	LogProvider LogField = "provider_id"
	LogAt       LogField = "at"
	LogTexts    LogField = "texts"
	LogTokens   LogField = "tokens"
	LogMillis   LogField = "millis"
	LogOutcome  LogField = "outcome"
)

// Pattern is one include or exclude pattern of a workspace.
type Pattern struct {
	WorkspaceID int64
	Kind        string // "include" | "exclude"
	Ord         int64
	Pattern     string
}

// GraphOnly is one of a workspace's graph_only patterns (ADR 1792015212 §2.2), in order.
type GraphOnly struct {
	WorkspaceID int64
	Ord         int64
	Pattern     string
}

// GraphOnlyField names a workspace_graph_only column.
type GraphOnlyField string

const (
	GraphOnlyWorkspace GraphOnlyField = wsCol
	GraphOnlyOrd       GraphOnlyField = "ord"
	GraphOnlyValue     GraphOnlyField = "pattern"
)

// PatternField names a workspace_pattern column.
type PatternField string

const (
	PatternWorkspace PatternField = wsCol
	PatternKind      PatternField = "kind"
	PatternOrd       PatternField = "ord"
	PatternValue     PatternField = "pattern"
)

// innerJoin joins table to from on the workspace, then table's col equal to
// from's key: a join between two workspace-owned tables is on both. leftJoin
// keeps a from row that joins none.
func innerJoin(table, col, from, key string) dao.Expr {
	return dao.InnerJoinOn(table, dao.On(dao.T(table, wsCol), dao.T(from, wsCol)), dao.On(dao.T(table, col), dao.T(from, key)))
}

func leftJoin(table, col, from, key string) dao.Expr {
	return dao.LeftJoinOn(table, dao.On(dao.T(table, wsCol), dao.T(from, wsCol)), dao.On(dao.T(table, col), dao.T(from, key)))
}

// Model is an embedding model a workspace used.
type Model struct {
	WorkspaceID    int64
	FP             string
	Provider, Name *string
	Dims           *int64
	Active         int64
	// Target is the model the workspace's indexer fills: the active one outside a switch (000015)
	Target int64
}

// ModelField names a model column.
type ModelField string

const (
	ModelWorkspace ModelField = wsCol
	ModelFP        ModelField = "fp"
	ModelProvider  ModelField = "provider"
	ModelName      ModelField = "name"
	ModelDims      ModelField = "dims"
	ModelActive    ModelField = "active"
	ModelTarget    ModelField = "target"
)

// Event is one configuration or lifecycle change, in the daemon-wide log (000009).
type Event struct {
	Seq                     int64
	Kind, Workspace, Client string
	Detail                  string
	At                      int64
}

// EventField names an event column.
type EventField string

const (
	EventSeq       EventField = "seq"
	EventKind      EventField = "kind"
	EventWorkspace EventField = "workspace"
	EventClient    EventField = "client"
	EventDetail    EventField = "detail"
	EventAt        EventField = "at"
)

// The sort a table without its own order uses.
type noSort string

// Sorts shared by the tables ordered by one column.
const (
	ByPath noSort = "path"
	BySeq  noSort = "seq"
	ByKey  noSort = "key"
)

// tables is every declaration, bound to one connection (a dao.Schema is).
type tables struct {
	workspaces  *dao.Schema[*Workspace, WorkspaceField, noSort, int64]
	preferences *dao.Schema[*Preference, PreferenceField, noSort, string]
	providers   *dao.Schema[*Provider, ProviderField, noSort, int64]
	usage       *dao.Schema[*Usage, UsageField, noSort, int64]
	calls       *dao.Schema[*LogEntry, LogField, noSort, int64]
	rankers     *dao.Schema[*Ranker, RankerField, noSort, int64]
	rankUsage   *dao.Schema[*Usage, UsageField, noSort, int64]
	rankCalls   *dao.Schema[*LogEntry, LogField, noSort, int64]
	patterns    *dao.Schema[*Pattern, PatternField, noSort, int64]
	graphOnly   *dao.Schema[*GraphOnly, GraphOnlyField, noSort, int64]
	connections *dao.Schema[*Connection, ConnectionField, noSort, int64]
	models      *dao.Schema[*Model, ModelField, noSort, string]
	events      *dao.Schema[*Event, EventField, noSort, int64]
	declared    []any // every package's own declared tables (declare.go)
}

func col[R any, T ~string](table string, c T, scan func(R) any) dao.Field[R] {
	return dao.Field[R]{Expr: dao.T(table, c), Scan: scan}
}

func joined[R any, T ~string](table string, c T, join dao.JoinKey, scan func(R) any) dao.Field[R] {
	return dao.Field[R]{Expr: dao.T(table, c), Join: join, ReadOnly: true, Scan: scan}
}

func newTables(c dao.DataConn) *tables {
	return &tables{
		declared: built(c),
		workspaces: dao.New[*Workspace, WorkspaceField, noSort, int64](c,
			dao.Table[*Workspace, WorkspaceField, noSort, int64]("workspace"),
			dao.ID[*Workspace, WorkspaceField, noSort, int64](WorkspaceID),
			dao.Fields[*Workspace, WorkspaceField, noSort, int64](map[WorkspaceField]dao.Field[*Workspace]{
				WorkspaceID:              col("workspace", WorkspaceID, func(w *Workspace) any { return &w.ID }),
				WorkspaceName:            col("workspace", WorkspaceName, func(w *Workspace) any { return &w.Name }),
				WorkspaceRoot:            col("workspace", WorkspaceRoot, func(w *Workspace) any { return &w.Root }),
				WorkspaceCommitSeq:       col("workspace", WorkspaceCommitSeq, func(w *Workspace) any { return &w.CommitSeq }),
				WorkspaceChangeSeq:       col("workspace", WorkspaceChangeSeq, func(w *Workspace) any { return &w.ChangeSeq }),
				WorkspaceSectionTokens:   col("workspace", WorkspaceSectionTokens, func(w *Workspace) any { return &w.SectionTokens }),
				WorkspaceEmbeddingPolicy: col("workspace", WorkspaceEmbeddingPolicy, func(w *Workspace) any { return &w.EmbeddingPolicy }),
				WorkspaceIncludeEmpty:    col("workspace", WorkspaceIncludeEmpty, func(w *Workspace) any { return &w.IncludeEmpty }),
				WorkspaceSchemaPath:      col("workspace", WorkspaceSchemaPath, func(w *Workspace) any { return &w.SchemaPath }),
				WorkspaceTextExtensions:  col("workspace", WorkspaceTextExtensions, func(w *Workspace) any { return &w.TextExtensions }),
				WorkspaceProviderID:      col("workspace", WorkspaceProviderID, func(w *Workspace) any { return &w.ProviderID }),
				WorkspaceUID:             col("workspace", WorkspaceUID, func(w *Workspace) any { return &w.UID }),
				WorkspaceDestination:     col("workspace", WorkspaceDestination, func(w *Workspace) any { return &w.Destination }),
				WorkspaceVectorIndex:     col("workspace", WorkspaceVectorIndex, func(w *Workspace) any { return &w.VectorIndex }),
				WorkspaceViewArgs:        col("workspace", WorkspaceViewArgs, func(w *Workspace) any { return &w.ViewArgs }),
				WorkspaceAbstractChunk:   col("workspace", WorkspaceAbstractChunk, func(w *Workspace) any { return &w.AbstractChunk }),
				WorkspaceGraphOnlySet:    col("workspace", WorkspaceGraphOnlySet, func(w *Workspace) any { return &w.GraphOnlySet }),
				WorkspaceTodoSearch:      col("workspace", WorkspaceTodoSearch, func(w *Workspace) any { return &w.TodoSearch }),
				WorkspaceDemote:          col("workspace", WorkspaceDemote, func(w *Workspace) any { return &w.DemoteSuperseded }),
				WorkspaceCreatedAt:       col("workspace", WorkspaceCreatedAt, func(w *Workspace) any { return &w.CreatedAt }),
				WorkspaceUpdatedAt:       col("workspace", WorkspaceUpdatedAt, func(w *Workspace) any { return &w.UpdatedAt }),
			}),
			dao.SortMap[*Workspace, WorkspaceField, noSort, int64](map[noSort]string{ByKey: `"workspace"."name"`})),
		preferences: dao.New[*Preference, PreferenceField, noSort, string](c,
			dao.Table[*Preference, PreferenceField, noSort, string]("preference"),
			dao.ID[*Preference, PreferenceField, noSort, string](PrefName),
			dao.Fields[*Preference, PreferenceField, noSort, string](map[PreferenceField]dao.Field[*Preference]{
				PrefName:      col("preference", PrefName, func(p *Preference) any { return &p.Name }),
				PrefValue:     col("preference", PrefValue, func(p *Preference) any { return &p.Value }),
				PrefUpdatedAt: col("preference", PrefUpdatedAt, func(p *Preference) any { return &p.UpdatedAt }),
			}),
			dao.Conflict[*Preference, PreferenceField, noSort, string](PrefName),
			dao.SortMap[*Preference, PreferenceField, noSort, string](map[noSort]string{ByKey: `"preference"."name"`})),
		providers: dao.New[*Provider, ProviderField, noSort, int64](c,
			dao.Table[*Provider, ProviderField, noSort, int64]("embedding_provider"),
			dao.ID[*Provider, ProviderField, noSort, int64](ProviderID),
			dao.Fields[*Provider, ProviderField, noSort, int64](map[ProviderField]dao.Field[*Provider]{
				ProviderID:        col("embedding_provider", ProviderID, func(p *Provider) any { return &p.ID }),
				ProviderName:      col("embedding_provider", ProviderName, func(p *Provider) any { return &p.Name }),
				ProviderKind:      col("embedding_provider", ProviderKind, func(p *Provider) any { return &p.Kind }),
				ProviderBaseURL:   col("embedding_provider", ProviderBaseURL, func(p *Provider) any { return &p.BaseURL }),
				ProviderModel:     col("embedding_provider", ProviderModel, func(p *Provider) any { return &p.Model }),
				ProviderContext:   col("embedding_provider", ProviderContext, func(p *Provider) any { return &p.Context }),
				ProviderAPIKey:    col("embedding_provider", ProviderAPIKey, func(p *Provider) any { return &p.APIKey }),
				ProviderCreatedAt: col("embedding_provider", ProviderCreatedAt, func(p *Provider) any { return &p.CreatedAt }),
				ProviderUpdatedAt: col("embedding_provider", ProviderUpdatedAt, func(p *Provider) any { return &p.UpdatedAt }),
			}),
			dao.SortMap[*Provider, ProviderField, noSort, int64](map[noSort]string{ByKey: `"embedding_provider"."name"`})),
		usage: dao.New[*Usage, UsageField, noSort, int64](c,
			dao.Table[*Usage, UsageField, noSort, int64]("embedding_usage"),
			dao.Fields[*Usage, UsageField, noSort, int64](map[UsageField]dao.Field[*Usage]{
				UsageProvider: col("embedding_usage", UsageProvider, func(u *Usage) any { return &u.ProviderID }),
				UsageDay:      col("embedding_usage", UsageDay, func(u *Usage) any { return &u.Day }),
				UsageRequests: col("embedding_usage", UsageRequests, func(u *Usage) any { return &u.Requests }),
				UsageTexts:    col("embedding_usage", UsageTexts, func(u *Usage) any { return &u.Texts }),
				UsageTokens:   col("embedding_usage", UsageTokens, func(u *Usage) any { return &u.Tokens }),
				UsageFailures: col("embedding_usage", UsageFailures, func(u *Usage) any { return &u.Failures }),
				UsageLimited:  col("embedding_usage", UsageLimited, func(u *Usage) any { return &u.Limited }),
			}),
			dao.Conflict[*Usage, UsageField, noSort, int64](UsageProvider, UsageDay),
			dao.SortMap[*Usage, UsageField, noSort, int64](map[noSort]string{ByKey: `"embedding_usage"."day"`})),
		calls: dao.New[*LogEntry, LogField, noSort, int64](c,
			dao.Table[*LogEntry, LogField, noSort, int64]("embedding_log"),
			dao.ID[*LogEntry, LogField, noSort, int64](LogID),
			dao.Fields[*LogEntry, LogField, noSort, int64](map[LogField]dao.Field[*LogEntry]{
				LogID:       col("embedding_log", LogID, func(e *LogEntry) any { return &e.ID }),
				LogProvider: col("embedding_log", LogProvider, func(e *LogEntry) any { return &e.ProviderID }),
				LogAt:       col("embedding_log", LogAt, func(e *LogEntry) any { return &e.At }),
				LogTexts:    col("embedding_log", LogTexts, func(e *LogEntry) any { return &e.Texts }),
				LogTokens:   col("embedding_log", LogTokens, func(e *LogEntry) any { return &e.Tokens }),
				LogMillis:   col("embedding_log", LogMillis, func(e *LogEntry) any { return &e.Millis }),
				LogOutcome:  col("embedding_log", LogOutcome, func(e *LogEntry) any { return &e.Outcome }),
			}),
			dao.SortMap[*LogEntry, LogField, noSort, int64](map[noSort]string{ByKey: `"embedding_log"."id"`})),
		rankers: dao.New[*Ranker, RankerField, noSort, int64](c,
			dao.Table[*Ranker, RankerField, noSort, int64]("ranker"),
			dao.ID[*Ranker, RankerField, noSort, int64](RankerID),
			dao.Fields[*Ranker, RankerField, noSort, int64](map[RankerField]dao.Field[*Ranker]{
				RankerID:        col("ranker", RankerID, func(r *Ranker) any { return &r.ID }),
				RankerName:      col("ranker", RankerName, func(r *Ranker) any { return &r.Name }),
				RankerKind:      col("ranker", RankerKind, func(r *Ranker) any { return &r.Kind }),
				RankerBaseURL:   col("ranker", RankerBaseURL, func(r *Ranker) any { return &r.BaseURL }),
				RankerModel:     col("ranker", RankerModel, func(r *Ranker) any { return &r.Model }),
				RankerAPIKey:    col("ranker", RankerAPIKey, func(r *Ranker) any { return &r.APIKey }),
				RankerCreatedAt: col("ranker", RankerCreatedAt, func(r *Ranker) any { return &r.CreatedAt }),
				RankerUpdatedAt: col("ranker", RankerUpdatedAt, func(r *Ranker) any { return &r.UpdatedAt }),
			}),
			dao.SortMap[*Ranker, RankerField, noSort, int64](map[noSort]string{ByKey: `"ranker"."name"`})),
		// a ranker's usage and log are an embedding provider's, in their own tables, ranker_id for
		// provider_id
		rankUsage: dao.New[*Usage, UsageField, noSort, int64](c,
			dao.Table[*Usage, UsageField, noSort, int64]("ranker_usage"),
			dao.Fields[*Usage, UsageField, noSort, int64](map[UsageField]dao.Field[*Usage]{
				UsageProvider: col("ranker_usage", "ranker_id", func(u *Usage) any { return &u.ProviderID }),
				UsageDay:      col("ranker_usage", UsageDay, func(u *Usage) any { return &u.Day }),
				UsageRequests: col("ranker_usage", UsageRequests, func(u *Usage) any { return &u.Requests }),
				UsageTexts:    col("ranker_usage", UsageTexts, func(u *Usage) any { return &u.Texts }),
				UsageTokens:   col("ranker_usage", UsageTokens, func(u *Usage) any { return &u.Tokens }),
				UsageFailures: col("ranker_usage", UsageFailures, func(u *Usage) any { return &u.Failures }),
				UsageLimited:  col("ranker_usage", UsageLimited, func(u *Usage) any { return &u.Limited }),
			}),
			dao.Conflict[*Usage, UsageField, noSort, int64](UsageProvider, UsageDay),
			dao.SortMap[*Usage, UsageField, noSort, int64](map[noSort]string{ByKey: `"ranker_usage"."day"`})),
		rankCalls: dao.New[*LogEntry, LogField, noSort, int64](c,
			dao.Table[*LogEntry, LogField, noSort, int64]("ranker_log"),
			dao.ID[*LogEntry, LogField, noSort, int64](LogID),
			dao.Fields[*LogEntry, LogField, noSort, int64](map[LogField]dao.Field[*LogEntry]{
				LogID:       col("ranker_log", LogID, func(e *LogEntry) any { return &e.ID }),
				LogProvider: col("ranker_log", "ranker_id", func(e *LogEntry) any { return &e.ProviderID }),
				LogAt:       col("ranker_log", LogAt, func(e *LogEntry) any { return &e.At }),
				LogTexts:    col("ranker_log", LogTexts, func(e *LogEntry) any { return &e.Texts }),
				LogTokens:   col("ranker_log", LogTokens, func(e *LogEntry) any { return &e.Tokens }),
				LogMillis:   col("ranker_log", LogMillis, func(e *LogEntry) any { return &e.Millis }),
				LogOutcome:  col("ranker_log", LogOutcome, func(e *LogEntry) any { return &e.Outcome }),
			}),
			dao.SortMap[*LogEntry, LogField, noSort, int64](map[noSort]string{ByKey: `"ranker_log"."id"`})),
		patterns: dao.New[*Pattern, PatternField, noSort, int64](c,
			dao.Table[*Pattern, PatternField, noSort, int64]("workspace_pattern"),
			dao.Fields[*Pattern, PatternField, noSort, int64](map[PatternField]dao.Field[*Pattern]{
				PatternWorkspace: col("workspace_pattern", PatternWorkspace, func(p *Pattern) any { return &p.WorkspaceID }),
				PatternKind:      col("workspace_pattern", PatternKind, func(p *Pattern) any { return &p.Kind }),
				PatternOrd:       col("workspace_pattern", PatternOrd, func(p *Pattern) any { return &p.Ord }),
				PatternValue:     col("workspace_pattern", PatternValue, func(p *Pattern) any { return &p.Pattern }),
			}),
			dao.SortMap[*Pattern, PatternField, noSort, int64](map[noSort]string{ByKey: `"workspace_pattern"."kind", "workspace_pattern"."ord"`})),
		graphOnly: dao.New[*GraphOnly, GraphOnlyField, noSort, int64](c,
			dao.Table[*GraphOnly, GraphOnlyField, noSort, int64]("workspace_graph_only"),
			dao.Fields[*GraphOnly, GraphOnlyField, noSort, int64](map[GraphOnlyField]dao.Field[*GraphOnly]{
				GraphOnlyWorkspace: col("workspace_graph_only", GraphOnlyWorkspace, func(g *GraphOnly) any { return &g.WorkspaceID }),
				GraphOnlyOrd:       col("workspace_graph_only", GraphOnlyOrd, func(g *GraphOnly) any { return &g.Ord }),
				GraphOnlyValue:     col("workspace_graph_only", GraphOnlyValue, func(g *GraphOnly) any { return &g.Pattern }),
			}),
			dao.SortMap[*GraphOnly, GraphOnlyField, noSort, int64](map[noSort]string{ByKey: `"workspace_graph_only"."ord"`})),
		connections: dao.New[*Connection, ConnectionField, noSort, int64](c,
			dao.Table[*Connection, ConnectionField, noSort, int64]("workspace_connection"),
			dao.Fields[*Connection, ConnectionField, noSort, int64](map[ConnectionField]dao.Field[*Connection]{
				ConnWorkspace: col("workspace_connection", ConnWorkspace, func(x *Connection) any { return &x.WorkspaceID }),
				ConnRole:      col("workspace_connection", ConnRole, func(x *Connection) any { return &x.Role }),
				ConnEngine:    col("workspace_connection", ConnEngine, func(x *Connection) any { return &x.Engine }),
				ConnDSN:       col("workspace_connection", ConnDSN, func(x *Connection) any { return &x.DSN }),
				ConnSchema:    col("workspace_connection", ConnSchema, func(x *Connection) any { return &x.Schema }),
				ConnUpdatedAt: col("workspace_connection", ConnUpdatedAt, func(x *Connection) any { return &x.UpdatedAt }),
			}),
			dao.Conflict[*Connection, ConnectionField, noSort, int64](ConnWorkspace, ConnRole),
			dao.SortMap[*Connection, ConnectionField, noSort, int64](map[noSort]string{ByKey: `"workspace_connection"."role"`})),
		models: dao.New[*Model, ModelField, noSort, string](c,
			dao.Table[*Model, ModelField, noSort, string]("model"),
			dao.Fields[*Model, ModelField, noSort, string](map[ModelField]dao.Field[*Model]{
				ModelWorkspace: col("model", ModelWorkspace, func(m *Model) any { return &m.WorkspaceID }),
				ModelFP:        col("model", ModelFP, func(m *Model) any { return &m.FP }),
				ModelProvider:  col("model", ModelProvider, func(m *Model) any { return &m.Provider }),
				ModelName:      col("model", ModelName, func(m *Model) any { return &m.Name }),
				ModelDims:      col("model", ModelDims, func(m *Model) any { return &m.Dims }),
				ModelActive:    col("model", ModelActive, func(m *Model) any { return &m.Active }),
				ModelTarget:    col("model", ModelTarget, func(m *Model) any { return &m.Target }),
			}),
			dao.Conflict[*Model, ModelField, noSort, string](ModelWorkspace, ModelFP)),
		events: dao.New[*Event, EventField, noSort, int64](c,
			dao.Table[*Event, EventField, noSort, int64]("event"),
			dao.ID[*Event, EventField, noSort, int64](EventSeq),
			dao.Fields[*Event, EventField, noSort, int64](map[EventField]dao.Field[*Event]{
				EventSeq:       col("event", EventSeq, func(e *Event) any { return &e.Seq }),
				EventKind:      col("event", EventKind, func(e *Event) any { return &e.Kind }),
				EventWorkspace: col("event", EventWorkspace, func(e *Event) any { return &e.Workspace }),
				EventClient:    col("event", EventClient, func(e *Event) any { return &e.Client }),
				EventDetail:    col("event", EventDetail, func(e *Event) any { return &e.Detail }),
				EventAt:        col("event", EventAt, func(e *Event) any { return &e.At }),
			}),
			dao.SortMap[*Event, EventField, noSort, int64](map[noSort]string{BySeq: `"event"."seq"`})),
	}
}
