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

// Usage is a provider's use on one day (UTC, YYYY-MM-DD).
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

// LogEntry is one of a provider's recent calls.
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

// PatternField names a workspace_pattern column.
type PatternField string

const (
	PatternWorkspace PatternField = wsCol
	PatternKind      PatternField = "kind"
	PatternOrd       PatternField = "ord"
	PatternValue     PatternField = "pattern"
)

// Document is one indexed file.
type Document struct {
	ID, WorkspaceID  int64
	Path, Version    string
	ActiveGen        int64
	SemanticReady    int64
	Title            *string
	FrontmatterJSON  *string
	FrontmatterError *string
	Indexer          string
	IndexedAt        int64
}

// DocumentField names a document column.
type DocumentField string

const (
	DocID               DocumentField = "id"
	DocWorkspace        DocumentField = wsCol
	DocPath             DocumentField = "path"
	DocVersion          DocumentField = "version"
	DocActiveGen        DocumentField = "active_gen"
	DocSemanticReady    DocumentField = "semantic_ready"
	DocTitle            DocumentField = "title"
	DocFrontmatterJSON  DocumentField = "frontmatter_json"
	DocFrontmatterError DocumentField = "frontmatter_error"
	DocIndexer          DocumentField = "indexer"
	DocIndexedAt        DocumentField = "indexed_at"
)

// Chunk is one section of a document, alive in [GenFrom, GenTo).
type Chunk struct {
	ID, WorkspaceID, DocID int64
	Hash, TextHash         []byte
	GenFrom                int64
	GenTo                  *int64
	Ord                    int64
	Breadcrumb, Body       string
	Embed                  string // the text its vector is made of, when not Breadcrumb + "\n" + Body (000012)
	Title, Tags            string
	ByteStart, ByteEnd     int64
	DocPath                string // joined: the document's path
	DocActiveGen           int64  // joined: the document's active generation
	DocReady               int64  // joined: the document's semantic_ready
	Snippet                string // joined: the full-text excerpt
	EmbBits, EmbF32        []byte // joined: the vector of the chunk's text under a model
}

// ChunkField names a chunk column.
type ChunkField string

const (
	ChunkID           ChunkField = "id"
	ChunkWorkspace    ChunkField = wsCol
	ChunkDoc          ChunkField = "doc_id"
	ChunkHash         ChunkField = "hash"
	ChunkTextHash     ChunkField = "text_hash"
	ChunkGenFrom      ChunkField = "gen_from"
	ChunkGenTo        ChunkField = "gen_to"
	ChunkOrd          ChunkField = "ord"
	ChunkBreadcrumb   ChunkField = "breadcrumb"
	ChunkBody         ChunkField = "body"
	ChunkEmbed        ChunkField = "embed"
	ChunkTitle        ChunkField = "title"
	ChunkTags         ChunkField = "tags"
	ChunkByteStart    ChunkField = "byte_start"
	ChunkByteEnd      ChunkField = "byte_end"
	ChunkDocPath      ChunkField = "doc_path"       // joined
	ChunkDocActiveGen ChunkField = "doc_active_gen" // joined
	ChunkDocReady     ChunkField = "doc_ready"      // joined
	ChunkSnippet      ChunkField = "snippet"        // joined, full text
	ChunkEmbBits      ChunkField = "emb_bits"       // joined, embedding
	ChunkEmbF32       ChunkField = "emb_f32"        // joined, embedding
)

// ChunkSort names a chunk order.
type ChunkSort string

const (
	ChunkByID   ChunkSort = "id"
	ChunkByOrd  ChunkSort = "ord"
	ChunkByPath ChunkSort = "path" // the document's path, then ord
	ChunkByRank ChunkSort = "rank" // full-text rank, best first
)

// The chunk declaration's joins. Each is on the composite key, the workspace
// with the row's own key (innerJoin): a joined row is always the chunk's
// workspace's, as the chunk is.
const (
	JoinDocument  dao.JoinKey = "document"
	JoinFTS       dao.JoinKey = "fts"
	JoinEmbedding dao.JoinKey = "embedding"
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

// The markers a chunk's full-text Snippet puts around each match: control
// characters, which no file's text holds.
const (
	HighlightStart = "\x02"
	HighlightEnd   = "\x03"
)

// ChunkFTS is the full-text index over chunk (the baseline's chunk_fts).
var ChunkFTS = dao.FullTextIndex{Name: "chunk_fts", Table: "chunk", Key: "id",
	Columns: []string{"title", "breadcrumb", "tags", "body", wsCol}}

// DocValue is one tag or alias of a document.
type DocValue struct {
	WorkspaceID, DocID int64
	Value              string
}

// DocValueField names a doc_tag or doc_alias column.
type DocValueField string

const (
	DocValueWorkspace DocValueField = wsCol
	DocValueDoc       DocValueField = "doc_id"
	DocValueValue     DocValueField = "value" // the tag or alias
)

// Facet is one typed value of a document's declared frontmatter field (000007).
type Facet struct {
	WorkspaceID, DocID int64
	Field, Value       string
}

// FacetField names a doc_facet column.
type FacetField string

const (
	FacetWorkspace FacetField = wsCol
	FacetDoc       FacetField = "doc_id"
	FacetName      FacetField = "field"
	FacetValue     FacetField = "value"
)

// Diagnostic is one problem with a document's frontmatter, in source order (000007).
type Diagnostic struct {
	WorkspaceID, DocID int64
	Ord                int64
	Field              string
	Line               int64
	Rule, Message      string
	DocPath            string // joined: the document's path
}

// DiagnosticField names a doc_diagnostic column.
type DiagnosticField string

const (
	DiagWorkspace DiagnosticField = wsCol
	DiagDoc       DiagnosticField = "doc_id"
	DiagOrd       DiagnosticField = "ord"
	DiagField     DiagnosticField = "field"
	DiagLine      DiagnosticField = "line"
	DiagRule      DiagnosticField = "rule"
	DiagMessage   DiagnosticField = "message"
	DiagDocPath   DiagnosticField = "doc_path" // joined
)

// DiagnosticSort names a doc_diagnostic order.
type DiagnosticSort string

const DiagByPath DiagnosticSort = "path" // the document's path, then ord

// DocName is a name a link can resolve through.
type DocName struct {
	WorkspaceID int64
	Key         string
	DocID       int64
	IsPath      int64
	DocPath     string // joined
}

// DocNameField names a doc_name column.
type DocNameField string

const (
	NameWorkspace DocNameField = wsCol
	NameKey       DocNameField = "name_key"
	NameDoc       DocNameField = "doc_id"
	NameIsPath    DocNameField = "is_path"
	NameDocPath   DocNameField = "doc_path" // joined
)

// Link is one link from a document.
type Link struct {
	ID, WorkspaceID, SrcDoc int64
	GenFrom                 int64
	GenTo                   *int64
	Raw, Name               string
	DstDoc                  *int64
	Anchor                  *string
	Kind                    string
	OtherPath               string // joined: the path of the document at the other end
}

// LinkField names a link column.
type LinkField string

const (
	LinkID        LinkField = "id"
	LinkWorkspace LinkField = wsCol
	LinkSrc       LinkField = "src_doc"
	LinkGenFrom   LinkField = "gen_from"
	LinkGenTo     LinkField = "gen_to"
	LinkRaw       LinkField = "raw"
	LinkName      LinkField = "name"
	LinkDst       LinkField = "dst_doc"
	LinkAnchor    LinkField = "anchor"
	LinkKind      LinkField = "kind"
	LinkOtherPath LinkField = "other_path" // joined
)

// LinkSort names a link order.
type LinkSort string

const (
	LinkByID        LinkSort = "id"
	LinkByOtherPath LinkSort = "other_path"
)

// The document at a link's other end: its target (links out) or its source (backlinks).
const JoinOther dao.JoinKey = "other"

// Model is an embedding model a workspace used.
type Model struct {
	WorkspaceID    int64
	FP             string
	Provider, Name *string
	Dims           *int64
	Active         int64
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
)

// Embedding is a text's vector under a model.
type Embedding struct {
	WorkspaceID int64
	TextHash    []byte
	ModelFP     string
	Bits, F32   []byte
}

// EmbeddingField names an embedding column.
type EmbeddingField string

const (
	EmbWorkspace EmbeddingField = wsCol
	EmbTextHash  EmbeddingField = "text_hash"
	EmbModel     EmbeddingField = "model_fp"
	EmbBits      EmbeddingField = "bits"
	EmbF32       EmbeddingField = "f32"
)

// Job is a file waiting to be indexed.
type Job struct {
	WorkspaceID int64
	Path        string
	Seq         int64
	Reason      *string
	EnqueuedAt  *int64
	Attempts    int64
	LastError   *string
}

// JobField names an index_job column.
type JobField string

const (
	JobWorkspace  JobField = wsCol
	JobPath       JobField = "path"
	JobSeq        JobField = "seq"
	JobReason     JobField = "reason"
	JobEnqueuedAt JobField = "enqueued_at"
	JobAttempts   JobField = "attempts"
	JobLastError  JobField = "last_error"
)

// Change is one row of a workspace's change log.
type Change struct {
	WorkspaceID int64
	Seq         int64
	Path, Op    string
	Generation  int64
	At          int64
}

// ChangeField names a change column.
type ChangeField string

const (
	ChangeWorkspace  ChangeField = wsCol
	ChangeSeq        ChangeField = "seq"
	ChangePath       ChangeField = "path"
	ChangeOp         ChangeField = "op"
	ChangeGeneration ChangeField = "generation"
	ChangeAt         ChangeField = "at"
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
	patterns    *dao.Schema[*Pattern, PatternField, noSort, int64]
	connections *dao.Schema[*Connection, ConnectionField, noSort, int64]
	documents   *dao.Schema[*Document, DocumentField, noSort, int64]
	chunks      *dao.Schema[*Chunk, ChunkField, ChunkSort, int64]
	tags        *dao.Schema[*DocValue, DocValueField, noSort, int64]
	aliases     *dao.Schema[*DocValue, DocValueField, noSort, int64]
	names       *dao.Schema[*DocName, DocNameField, noSort, int64]
	facets      *dao.Schema[*Facet, FacetField, noSort, int64]
	diagnostics *dao.Schema[*Diagnostic, DiagnosticField, DiagnosticSort, int64]
	linksOut    *dao.Schema[*Link, LinkField, LinkSort, int64] // joined to the target
	linksIn     *dao.Schema[*Link, LinkField, LinkSort, int64] // joined to the source
	models      *dao.Schema[*Model, ModelField, noSort, string]
	embeddings  *dao.Schema[*Embedding, EmbeddingField, noSort, string]
	jobs        *dao.Schema[*Job, JobField, noSort, string]
	changes     *dao.Schema[*Change, ChangeField, noSort, int64]
	events      *dao.Schema[*Event, EventField, noSort, int64]
}

func col[R any, T ~string](table string, c T, scan func(R) any) dao.Field[R] {
	return dao.Field[R]{Expr: dao.T(table, c), Scan: scan}
}

func joined[R any, T ~string](table string, c T, join dao.JoinKey, scan func(R) any) dao.Field[R] {
	return dao.Field[R]{Expr: dao.T(table, c), Join: join, ReadOnly: true, Scan: scan}
}

func newTables(c dao.DataConn) *tables {
	return &tables{
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
		patterns: dao.New[*Pattern, PatternField, noSort, int64](c,
			dao.Table[*Pattern, PatternField, noSort, int64]("workspace_pattern"),
			dao.Fields[*Pattern, PatternField, noSort, int64](map[PatternField]dao.Field[*Pattern]{
				PatternWorkspace: col("workspace_pattern", PatternWorkspace, func(p *Pattern) any { return &p.WorkspaceID }),
				PatternKind:      col("workspace_pattern", PatternKind, func(p *Pattern) any { return &p.Kind }),
				PatternOrd:       col("workspace_pattern", PatternOrd, func(p *Pattern) any { return &p.Ord }),
				PatternValue:     col("workspace_pattern", PatternValue, func(p *Pattern) any { return &p.Pattern }),
			}),
			dao.SortMap[*Pattern, PatternField, noSort, int64](map[noSort]string{ByKey: `"workspace_pattern"."kind", "workspace_pattern"."ord"`})),
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
		documents: dao.New[*Document, DocumentField, noSort, int64](c,
			dao.Table[*Document, DocumentField, noSort, int64]("document"),
			dao.ID[*Document, DocumentField, noSort, int64](DocID),
			dao.Fields[*Document, DocumentField, noSort, int64](map[DocumentField]dao.Field[*Document]{
				DocID:               col("document", DocID, func(d *Document) any { return &d.ID }),
				DocWorkspace:        col("document", DocWorkspace, func(d *Document) any { return &d.WorkspaceID }),
				DocPath:             col("document", DocPath, func(d *Document) any { return &d.Path }),
				DocVersion:          col("document", DocVersion, func(d *Document) any { return &d.Version }),
				DocActiveGen:        col("document", DocActiveGen, func(d *Document) any { return &d.ActiveGen }),
				DocSemanticReady:    col("document", DocSemanticReady, func(d *Document) any { return &d.SemanticReady }),
				DocTitle:            col("document", DocTitle, func(d *Document) any { return &d.Title }),
				DocFrontmatterJSON:  col("document", DocFrontmatterJSON, func(d *Document) any { return &d.FrontmatterJSON }),
				DocFrontmatterError: col("document", DocFrontmatterError, func(d *Document) any { return &d.FrontmatterError }),
				DocIndexer:          col("document", DocIndexer, func(d *Document) any { return &d.Indexer }),
				DocIndexedAt:        col("document", DocIndexedAt, func(d *Document) any { return &d.IndexedAt }),
			}),
			dao.SortMap[*Document, DocumentField, noSort, int64](map[noSort]string{ByPath: `"document"."path"`})),
		chunks: dao.New[*Chunk, ChunkField, ChunkSort, int64](c,
			dao.Table[*Chunk, ChunkField, ChunkSort, int64]("chunk"),
			dao.ID[*Chunk, ChunkField, ChunkSort, int64](ChunkID),
			dao.Fields[*Chunk, ChunkField, ChunkSort, int64](map[ChunkField]dao.Field[*Chunk]{
				ChunkID:           col("chunk", ChunkID, func(x *Chunk) any { return &x.ID }),
				ChunkWorkspace:    col("chunk", ChunkWorkspace, func(x *Chunk) any { return &x.WorkspaceID }),
				ChunkDoc:          col("chunk", ChunkDoc, func(x *Chunk) any { return &x.DocID }),
				ChunkHash:         col("chunk", ChunkHash, func(x *Chunk) any { return &x.Hash }),
				ChunkTextHash:     col("chunk", ChunkTextHash, func(x *Chunk) any { return &x.TextHash }),
				ChunkGenFrom:      col("chunk", ChunkGenFrom, func(x *Chunk) any { return &x.GenFrom }),
				ChunkGenTo:        col("chunk", ChunkGenTo, func(x *Chunk) any { return &x.GenTo }),
				ChunkOrd:          col("chunk", ChunkOrd, func(x *Chunk) any { return &x.Ord }),
				ChunkBreadcrumb:   col("chunk", ChunkBreadcrumb, func(x *Chunk) any { return &x.Breadcrumb }),
				ChunkBody:         col("chunk", ChunkBody, func(x *Chunk) any { return &x.Body }),
				ChunkEmbed:        col("chunk", ChunkEmbed, func(x *Chunk) any { return &x.Embed }),
				ChunkTitle:        col("chunk", ChunkTitle, func(x *Chunk) any { return &x.Title }),
				ChunkTags:         col("chunk", ChunkTags, func(x *Chunk) any { return &x.Tags }),
				ChunkByteStart:    col("chunk", ChunkByteStart, func(x *Chunk) any { return &x.ByteStart }),
				ChunkByteEnd:      col("chunk", ChunkByteEnd, func(x *Chunk) any { return &x.ByteEnd }),
				ChunkDocPath:      joined("document", "path", JoinDocument, func(x *Chunk) any { return &x.DocPath }),
				ChunkDocActiveGen: joined("document", "active_gen", JoinDocument, func(x *Chunk) any { return &x.DocActiveGen }),
				ChunkDocReady:     joined("document", "semantic_ready", JoinDocument, func(x *Chunk) any { return &x.DocReady }),
				ChunkSnippet: {Expr: dao.Snippet(ChunkFTS, "body", dao.SnippetMarks{Open: HighlightStart, Close: HighlightEnd, Ellipsis: "…", Tokens: 16}),
					Join: JoinFTS, ReadOnly: true, Scan: func(x *Chunk) any { return &x.Snippet }},
				ChunkEmbBits: joined("embedding", "bits", JoinEmbedding, func(x *Chunk) any { return &x.EmbBits }),
				ChunkEmbF32:  joined("embedding", "f32", JoinEmbedding, func(x *Chunk) any { return &x.EmbF32 }),
			}),
			dao.OptionalJoinExpr[*Chunk, ChunkField, ChunkSort, int64](JoinDocument,
				innerJoin("document", "id", "chunk", "doc_id")),
			dao.OptionalJoinExpr[*Chunk, ChunkField, ChunkSort, int64](JoinFTS, dao.FullTextJoin(ChunkFTS)),
			dao.OptionalJoinExpr[*Chunk, ChunkField, ChunkSort, int64](JoinEmbedding,
				innerJoin("embedding", "text_hash", "chunk", "text_hash")),
			dao.SortMap[*Chunk, ChunkField, ChunkSort, int64](map[ChunkSort]string{
				ChunkByID:   `"chunk"."id"`,
				ChunkByOrd:  `"chunk"."ord"`,
				ChunkByPath: `"document"."path", "chunk"."ord"`,
			}),
			dao.JoinForSort[*Chunk, ChunkField, ChunkSort, int64](ChunkByPath, JoinDocument),
			dao.SortExpr[*Chunk, ChunkField, ChunkSort, int64](ChunkByRank, dao.Rank(ChunkFTS, 10, 5, 5, 1, 0)),
			dao.JoinForSort[*Chunk, ChunkField, ChunkSort, int64](ChunkByRank, JoinFTS)),
		tags:    docValues(c, "doc_tag", "tag"),
		aliases: docValues(c, "doc_alias", "alias"),
		names: dao.New[*DocName, DocNameField, noSort, int64](c,
			dao.Table[*DocName, DocNameField, noSort, int64]("doc_name"),
			dao.Fields[*DocName, DocNameField, noSort, int64](map[DocNameField]dao.Field[*DocName]{
				NameWorkspace: col("doc_name", NameWorkspace, func(n *DocName) any { return &n.WorkspaceID }),
				NameKey:       col("doc_name", NameKey, func(n *DocName) any { return &n.Key }),
				NameDoc:       col("doc_name", NameDoc, func(n *DocName) any { return &n.DocID }),
				NameIsPath:    col("doc_name", NameIsPath, func(n *DocName) any { return &n.IsPath }),
				NameDocPath:   joined("document", "path", JoinDocument, func(n *DocName) any { return &n.DocPath }),
			}),
			dao.OptionalJoinExpr[*DocName, DocNameField, noSort, int64](JoinDocument,
				innerJoin("document", "id", "doc_name", "doc_id"))),
		facets: dao.New[*Facet, FacetField, noSort, int64](c,
			dao.Table[*Facet, FacetField, noSort, int64]("doc_facet"),
			dao.Fields[*Facet, FacetField, noSort, int64](map[FacetField]dao.Field[*Facet]{
				FacetWorkspace: col("doc_facet", FacetWorkspace, func(f *Facet) any { return &f.WorkspaceID }),
				FacetDoc:       col("doc_facet", FacetDoc, func(f *Facet) any { return &f.DocID }),
				FacetName:      col("doc_facet", FacetName, func(f *Facet) any { return &f.Field }),
				FacetValue:     col("doc_facet", FacetValue, func(f *Facet) any { return &f.Value }),
			}),
			dao.Conflict[*Facet, FacetField, noSort, int64](FacetWorkspace, FacetName, FacetValue, FacetDoc),
			dao.SortMap[*Facet, FacetField, noSort, int64](map[noSort]string{ByKey: `"doc_facet"."field", "doc_facet"."value"`})),
		diagnostics: dao.New[*Diagnostic, DiagnosticField, DiagnosticSort, int64](c,
			dao.Table[*Diagnostic, DiagnosticField, DiagnosticSort, int64]("doc_diagnostic"),
			dao.Fields[*Diagnostic, DiagnosticField, DiagnosticSort, int64](map[DiagnosticField]dao.Field[*Diagnostic]{
				DiagWorkspace: col("doc_diagnostic", DiagWorkspace, func(d *Diagnostic) any { return &d.WorkspaceID }),
				DiagDoc:       col("doc_diagnostic", DiagDoc, func(d *Diagnostic) any { return &d.DocID }),
				DiagOrd:       col("doc_diagnostic", DiagOrd, func(d *Diagnostic) any { return &d.Ord }),
				DiagField:     col("doc_diagnostic", DiagField, func(d *Diagnostic) any { return &d.Field }),
				DiagLine:      col("doc_diagnostic", DiagLine, func(d *Diagnostic) any { return &d.Line }),
				DiagRule:      col("doc_diagnostic", DiagRule, func(d *Diagnostic) any { return &d.Rule }),
				DiagMessage:   col("doc_diagnostic", DiagMessage, func(d *Diagnostic) any { return &d.Message }),
				DiagDocPath:   joined("document", "path", JoinDocument, func(d *Diagnostic) any { return &d.DocPath }),
			}),
			dao.OptionalJoinExpr[*Diagnostic, DiagnosticField, DiagnosticSort, int64](JoinDocument,
				innerJoin("document", "id", "doc_diagnostic", "doc_id")),
			dao.SortMap[*Diagnostic, DiagnosticField, DiagnosticSort, int64](map[DiagnosticSort]string{
				DiagByPath: `"document"."path", "doc_diagnostic"."ord"`,
			}),
			dao.JoinForSort[*Diagnostic, DiagnosticField, DiagnosticSort, int64](DiagByPath, JoinDocument)),
		linksOut: links(c, "dst_doc", true),
		linksIn:  links(c, "src_doc", false),
		models: dao.New[*Model, ModelField, noSort, string](c,
			dao.Table[*Model, ModelField, noSort, string]("model"),
			dao.Fields[*Model, ModelField, noSort, string](map[ModelField]dao.Field[*Model]{
				ModelWorkspace: col("model", ModelWorkspace, func(m *Model) any { return &m.WorkspaceID }),
				ModelFP:        col("model", ModelFP, func(m *Model) any { return &m.FP }),
				ModelProvider:  col("model", ModelProvider, func(m *Model) any { return &m.Provider }),
				ModelName:      col("model", ModelName, func(m *Model) any { return &m.Name }),
				ModelDims:      col("model", ModelDims, func(m *Model) any { return &m.Dims }),
				ModelActive:    col("model", ModelActive, func(m *Model) any { return &m.Active }),
			}),
			dao.Conflict[*Model, ModelField, noSort, string](ModelWorkspace, ModelFP)),
		embeddings: dao.New[*Embedding, EmbeddingField, noSort, string](c,
			dao.Table[*Embedding, EmbeddingField, noSort, string]("embedding"),
			dao.Fields[*Embedding, EmbeddingField, noSort, string](map[EmbeddingField]dao.Field[*Embedding]{
				EmbWorkspace: col("embedding", EmbWorkspace, func(e *Embedding) any { return &e.WorkspaceID }),
				EmbTextHash:  col("embedding", EmbTextHash, func(e *Embedding) any { return &e.TextHash }),
				EmbModel:     col("embedding", EmbModel, func(e *Embedding) any { return &e.ModelFP }),
				EmbBits:      col("embedding", EmbBits, func(e *Embedding) any { return &e.Bits }),
				EmbF32:       col("embedding", EmbF32, func(e *Embedding) any { return &e.F32 }),
			}),
			dao.Conflict[*Embedding, EmbeddingField, noSort, string](EmbWorkspace, EmbTextHash, EmbModel)),
		jobs: dao.New[*Job, JobField, noSort, string](c,
			dao.Table[*Job, JobField, noSort, string]("index_job"),
			dao.Fields[*Job, JobField, noSort, string](map[JobField]dao.Field[*Job]{
				JobWorkspace:  col("index_job", JobWorkspace, func(j *Job) any { return &j.WorkspaceID }),
				JobPath:       col("index_job", JobPath, func(j *Job) any { return &j.Path }),
				JobSeq:        col("index_job", JobSeq, func(j *Job) any { return &j.Seq }),
				JobReason:     col("index_job", JobReason, func(j *Job) any { return &j.Reason }),
				JobEnqueuedAt: col("index_job", JobEnqueuedAt, func(j *Job) any { return &j.EnqueuedAt }),
				JobAttempts:   col("index_job", JobAttempts, func(j *Job) any { return &j.Attempts }),
				JobLastError:  col("index_job", JobLastError, func(j *Job) any { return &j.LastError }),
			}),
			dao.Conflict[*Job, JobField, noSort, string](JobWorkspace, JobPath),
			dao.SortMap[*Job, JobField, noSort, string](map[noSort]string{ByPath: `"index_job"."path"`, BySeq: `"index_job"."seq"`})),
		changes: dao.New[*Change, ChangeField, noSort, int64](c,
			dao.Table[*Change, ChangeField, noSort, int64]("change"),
			dao.Fields[*Change, ChangeField, noSort, int64](map[ChangeField]dao.Field[*Change]{
				ChangeWorkspace:  col("change", ChangeWorkspace, func(x *Change) any { return &x.WorkspaceID }),
				ChangeSeq:        col("change", ChangeSeq, func(x *Change) any { return &x.Seq }),
				ChangePath:       col("change", ChangePath, func(x *Change) any { return &x.Path }),
				ChangeOp:         col("change", ChangeOp, func(x *Change) any { return &x.Op }),
				ChangeGeneration: col("change", ChangeGeneration, func(x *Change) any { return &x.Generation }),
				ChangeAt:         col("change", ChangeAt, func(x *Change) any { return &x.At }),
			}),
			dao.SortMap[*Change, ChangeField, noSort, int64](map[noSort]string{BySeq: `"change"."seq"`})),
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

// docValues is doc_tag or doc_alias: one declaration, two tables.
func docValues(c dao.DataConn, table, valueCol string) *dao.Schema[*DocValue, DocValueField, noSort, int64] {
	return dao.New[*DocValue, DocValueField, noSort, int64](c,
		dao.Table[*DocValue, DocValueField, noSort, int64](table),
		dao.Fields[*DocValue, DocValueField, noSort, int64](map[DocValueField]dao.Field[*DocValue]{
			DocValueWorkspace: col(table, DocValueWorkspace, func(v *DocValue) any { return &v.WorkspaceID }),
			DocValueDoc:       col(table, DocValueDoc, func(v *DocValue) any { return &v.DocID }),
			DocValueValue:     col(table, valueCol, func(v *DocValue) any { return &v.Value }),
		}),
		dao.Conflict[*DocValue, DocValueField, noSort, int64](DocValueWorkspace, DocValueValue, DocValueDoc),
		dao.SortMap[*DocValue, DocValueField, noSort, int64](map[noSort]string{ByKey: `"` + table + `"."` + valueCol + `"`}))
}

// links is the link table joined to the document at one end: dst_doc for the
// targets of a document's links (a LEFT JOIN, since a link may resolve to
// none), src_doc for the sources of its backlinks.
func links(c dao.DataConn, end string, left bool) *dao.Schema[*Link, LinkField, LinkSort, int64] {
	join := innerJoin("document", "id", "link", end)
	if left {
		join = leftJoin("document", "id", "link", end)
	}
	return dao.New[*Link, LinkField, LinkSort, int64](c,
		dao.Table[*Link, LinkField, LinkSort, int64]("link"),
		dao.ID[*Link, LinkField, LinkSort, int64](LinkID),
		dao.Fields[*Link, LinkField, LinkSort, int64](map[LinkField]dao.Field[*Link]{
			LinkID:        col("link", LinkID, func(l *Link) any { return &l.ID }),
			LinkWorkspace: col("link", LinkWorkspace, func(l *Link) any { return &l.WorkspaceID }),
			LinkSrc:       col("link", LinkSrc, func(l *Link) any { return &l.SrcDoc }),
			LinkGenFrom:   col("link", LinkGenFrom, func(l *Link) any { return &l.GenFrom }),
			LinkGenTo:     col("link", LinkGenTo, func(l *Link) any { return &l.GenTo }),
			LinkRaw:       col("link", LinkRaw, func(l *Link) any { return &l.Raw }),
			LinkName:      col("link", LinkName, func(l *Link) any { return &l.Name }),
			LinkDst:       col("link", LinkDst, func(l *Link) any { return &l.DstDoc }),
			LinkAnchor:    col("link", LinkAnchor, func(l *Link) any { return &l.Anchor }),
			LinkKind:      col("link", LinkKind, func(l *Link) any { return &l.Kind }),
			LinkOtherPath: {Expr: dao.Coalesce(dao.T("document", "path"), dao.SQL("''")), Join: JoinOther, ReadOnly: true,
				Scan: func(l *Link) any { return &l.OtherPath }},
		}),
		dao.OptionalJoinExpr[*Link, LinkField, LinkSort, int64](JoinOther, join),
		dao.SortMap[*Link, LinkField, LinkSort, int64](map[LinkSort]string{
			LinkByID:        `"link"."id"`,
			LinkByOtherPath: `"document"."path", "link"."id"`,
		}),
		dao.JoinForSort[*Link, LinkField, LinkSort, int64](LinkByOtherPath, JoinOther))
}
