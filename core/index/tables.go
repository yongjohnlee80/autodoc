package index

import (
	"github.com/yongjohnlee80/golib/dao"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// THE INDEX'S TABLES — the rows of a workspace's index, declared to the store through its seam
// (store.Declare): every store builds them for its writer and its readers. The tables are the
// migrations' (sql/deployments); this is their Go side, owned by the package that reads and writes
// them. Every one is a workspace's: it is reached through the Scope's stamp (store.Scoped), and a
// join between two of them is on the workspace too (store.InnerJoin).

// noSort is the order of a table ordered by one column.
type noSort string

const (
	byPath noSort = "path"
	// index.documents' orders, by their stored ascending keys (ADR 1793312720 D1)
	byUpdatedKey noSort = "updated_key"
	byIndexedKey noSort = "indexed_key"
	bySeq        noSort = "seq"
	byKey        noSort = "key"
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
	// the sort keys index.documents orders by (ADR 1793312720 D2, docKey); nil until written
	KUpdS, KUpdNs, KIndexed *int64
}

// DocumentField names a document column.
type DocumentField string

const (
	DocID               DocumentField = "id"
	DocWorkspace        DocumentField = store.WorkspaceColumn
	DocPath             DocumentField = "path"
	DocVersion          DocumentField = "version"
	DocActiveGen        DocumentField = "active_gen"
	DocSemanticReady    DocumentField = "semantic_ready"
	DocTitle            DocumentField = "title"
	DocFrontmatterJSON  DocumentField = "frontmatter_json"
	DocFrontmatterError DocumentField = "frontmatter_error"
	DocIndexer          DocumentField = "indexer"
	DocIndexedAt        DocumentField = "indexed_at"
	DocKUpdS            DocumentField = "k_upd_s"
	DocKUpdNs           DocumentField = "k_upd_ns"
	DocKIndexed         DocumentField = "k_indexed"
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
	Kind                   string // "section", or "abstract": the document's abstract (000014)
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
	ChunkWorkspace    ChunkField = store.WorkspaceColumn
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
	ChunkKind         ChunkField = "kind"
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

// HighlightStart and HighlightEnd mark the matched terms in a chunk's full-text Snippet, and so in
// a Hit's: control characters, which no file contains, so a client can render them as it likes.
const (
	HighlightStart = "\x02"
	HighlightEnd   = "\x03"
)

// ChunkFTS is the full-text index over chunk (the baseline's chunk_fts).
var ChunkFTS = dao.FullTextIndex{Name: "chunk_fts", Table: "chunk", Key: "id",
	Columns: []string{"title", "breadcrumb", "tags", "body", store.WorkspaceColumn}}

// DocValue is one tag or alias of a document.
type DocValue struct {
	WorkspaceID, DocID int64
	Value              string
}

// DocValueField names a doc_tag or doc_alias column.
type DocValueField string

const (
	DocValueWorkspace DocValueField = store.WorkspaceColumn
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
	FacetWorkspace FacetField = store.WorkspaceColumn
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
	DiagWorkspace DiagnosticField = store.WorkspaceColumn
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
	NameWorkspace DocNameField = store.WorkspaceColumn
	NameKey       DocNameField = "name_key"
	NameDoc       DocNameField = "doc_id"
	NameIsPath    DocNameField = "is_path"
	NameDocPath   DocNameField = "doc_path" // joined
)

// LinkKey is one of the names a relation link is looked up under, tried in Ord order: a
// frontmatter relation may name its target as a path from the workspace root and from its own
// folder, and resolves to the first that exists.
type LinkKey struct {
	WorkspaceID int64
	LinkID      int64
	Ord         int64
	Key         string
}

// LinkKeyField names a link_key column.
type LinkKeyField string

const (
	LinkKeyWorkspace LinkKeyField = store.WorkspaceColumn
	LinkKeyLink      LinkKeyField = "link_id"
	LinkKeyOrd       LinkKeyField = "ord"
	LinkKeyKey       LinkKeyField = "key"
)

// LinkRow is one link from a document.
type LinkRow struct {
	ID, WorkspaceID, SrcDoc int64
	GenFrom                 int64
	GenTo                   *int64
	Raw, Name               string
	DstDoc                  *int64
	Anchor                  *string
	Kind                    string
	Target                  *string // an external link's file, absolute (ADR 1792015212 §2.4); nil otherwise
	OtherPath               string  // joined: the path of the document at the other end
}

// LinkField names a link column.
type LinkField string

const (
	LinkID        LinkField = "id"
	LinkWorkspace LinkField = store.WorkspaceColumn
	LinkSrc       LinkField = "src_doc"
	LinkGenFrom   LinkField = "gen_from"
	LinkGenTo     LinkField = "gen_to"
	LinkRaw       LinkField = "raw"
	LinkName      LinkField = "name"
	LinkDst       LinkField = "dst_doc"
	LinkAnchor    LinkField = "anchor"
	LinkKind      LinkField = "kind"
	LinkTarget    LinkField = "target"
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
	EmbWorkspace EmbeddingField = store.WorkspaceColumn
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
	JobWorkspace  JobField = store.WorkspaceColumn
	JobPath       JobField = "path"
	JobSeq        JobField = "seq"
	JobReason     JobField = "reason"
	JobEnqueuedAt JobField = "enqueued_at"
	JobAttempts   JobField = "attempts"
	JobLastError  JobField = "last_error"
)

// ChangeRow is one row of a workspace's change log.
type ChangeRow struct {
	WorkspaceID int64
	Seq         int64
	Path, Op    string
	Generation  int64
	At          int64
}

// ChangeField names a change column.
type ChangeField string

const (
	ChangeWorkspace  ChangeField = store.WorkspaceColumn
	ChangeSeq        ChangeField = "seq"
	ChangePath       ChangeField = "path"
	ChangeOp         ChangeField = "op"
	ChangeGeneration ChangeField = "generation"
	ChangeAt         ChangeField = "at"
)

// indexTables is every declaration, bound to one connection.
type indexTables struct {
	documents   *dao.Schema[*Document, DocumentField, noSort, int64]
	chunks      *dao.Schema[*Chunk, ChunkField, ChunkSort, int64]
	tags        *dao.Schema[*DocValue, DocValueField, noSort, int64]
	aliases     *dao.Schema[*DocValue, DocValueField, noSort, int64]
	names       *dao.Schema[*DocName, DocNameField, noSort, int64]
	linkKeys    *dao.Schema[*LinkKey, LinkKeyField, noSort, int64]
	facets      *dao.Schema[*Facet, FacetField, noSort, int64]
	diagnostics *dao.Schema[*Diagnostic, DiagnosticField, DiagnosticSort, int64]
	linksOut    *dao.Schema[*LinkRow, LinkField, LinkSort, int64] // joined to the target
	linksIn     *dao.Schema[*LinkRow, LinkField, LinkSort, int64] // joined to the source
	embeddings  *dao.Schema[*Embedding, EmbeddingField, noSort, string]
	jobs        *dao.Schema[*Job, JobField, noSort, string]
	changes     *dao.Schema[*ChangeRow, ChangeField, noSort, int64]
}

// tables are the index's, declared to the store.
var tables = store.Declare("index", newIndexTables)

func newIndexTables(c dao.DataConn) *indexTables {
	return &indexTables{
		documents: dao.New[*Document, DocumentField, noSort, int64](c,
			dao.Table[*Document, DocumentField, noSort, int64]("document"),
			dao.ID[*Document, DocumentField, noSort, int64](DocID),
			dao.Fields[*Document, DocumentField, noSort, int64](map[DocumentField]dao.Field[*Document]{
				DocID:               store.Col("document", DocID, func(d *Document) any { return &d.ID }),
				DocWorkspace:        store.Col("document", DocWorkspace, func(d *Document) any { return &d.WorkspaceID }),
				DocPath:             store.Col("document", DocPath, func(d *Document) any { return &d.Path }),
				DocVersion:          store.Col("document", DocVersion, func(d *Document) any { return &d.Version }),
				DocActiveGen:        store.Col("document", DocActiveGen, func(d *Document) any { return &d.ActiveGen }),
				DocSemanticReady:    store.Col("document", DocSemanticReady, func(d *Document) any { return &d.SemanticReady }),
				DocTitle:            store.Col("document", DocTitle, func(d *Document) any { return &d.Title }),
				DocFrontmatterJSON:  store.Col("document", DocFrontmatterJSON, func(d *Document) any { return &d.FrontmatterJSON }),
				DocFrontmatterError: store.Col("document", DocFrontmatterError, func(d *Document) any { return &d.FrontmatterError }),
				DocIndexer:          store.Col("document", DocIndexer, func(d *Document) any { return &d.Indexer }),
				DocIndexedAt:        store.Col("document", DocIndexedAt, func(d *Document) any { return &d.IndexedAt }),
				DocKUpdS:            store.Col("document", DocKUpdS, func(d *Document) any { return &d.KUpdS }),
				DocKUpdNs:           store.Col("document", DocKUpdNs, func(d *Document) any { return &d.KUpdNs }),
				DocKIndexed:         store.Col("document", DocKIndexed, func(d *Document) any { return &d.KIndexed }),
			}),
			dao.SortMap[*Document, DocumentField, noSort, int64](map[noSort]string{byPath: `"document"."path"`,
				byUpdatedKey: `"document"."k_upd_s", "document"."k_upd_ns", "document"."path"`,
				byIndexedKey: `"document"."k_indexed", "document"."path"`})),
		chunks: dao.New[*Chunk, ChunkField, ChunkSort, int64](c,
			dao.Table[*Chunk, ChunkField, ChunkSort, int64]("chunk"),
			dao.ID[*Chunk, ChunkField, ChunkSort, int64](ChunkID),
			dao.Fields[*Chunk, ChunkField, ChunkSort, int64](map[ChunkField]dao.Field[*Chunk]{
				ChunkID:           store.Col("chunk", ChunkID, func(x *Chunk) any { return &x.ID }),
				ChunkWorkspace:    store.Col("chunk", ChunkWorkspace, func(x *Chunk) any { return &x.WorkspaceID }),
				ChunkDoc:          store.Col("chunk", ChunkDoc, func(x *Chunk) any { return &x.DocID }),
				ChunkHash:         store.Col("chunk", ChunkHash, func(x *Chunk) any { return &x.Hash }),
				ChunkTextHash:     store.Col("chunk", ChunkTextHash, func(x *Chunk) any { return &x.TextHash }),
				ChunkGenFrom:      store.Col("chunk", ChunkGenFrom, func(x *Chunk) any { return &x.GenFrom }),
				ChunkGenTo:        store.Col("chunk", ChunkGenTo, func(x *Chunk) any { return &x.GenTo }),
				ChunkOrd:          store.Col("chunk", ChunkOrd, func(x *Chunk) any { return &x.Ord }),
				ChunkBreadcrumb:   store.Col("chunk", ChunkBreadcrumb, func(x *Chunk) any { return &x.Breadcrumb }),
				ChunkBody:         store.Col("chunk", ChunkBody, func(x *Chunk) any { return &x.Body }),
				ChunkEmbed:        store.Col("chunk", ChunkEmbed, func(x *Chunk) any { return &x.Embed }),
				ChunkTitle:        store.Col("chunk", ChunkTitle, func(x *Chunk) any { return &x.Title }),
				ChunkTags:         store.Col("chunk", ChunkTags, func(x *Chunk) any { return &x.Tags }),
				ChunkByteStart:    store.Col("chunk", ChunkByteStart, func(x *Chunk) any { return &x.ByteStart }),
				ChunkByteEnd:      store.Col("chunk", ChunkByteEnd, func(x *Chunk) any { return &x.ByteEnd }),
				ChunkKind:         store.Col("chunk", ChunkKind, func(x *Chunk) any { return &x.Kind }),
				ChunkDocPath:      store.Joined("document", "path", JoinDocument, func(x *Chunk) any { return &x.DocPath }),
				ChunkDocActiveGen: store.Joined("document", "active_gen", JoinDocument, func(x *Chunk) any { return &x.DocActiveGen }),
				ChunkDocReady:     store.Joined("document", "semantic_ready", JoinDocument, func(x *Chunk) any { return &x.DocReady }),
				ChunkSnippet: {Expr: dao.Snippet(ChunkFTS, "body", dao.SnippetMarks{Open: HighlightStart, Close: HighlightEnd, Ellipsis: "…", Tokens: 16}),
					Join: JoinFTS, ReadOnly: true, Scan: func(x *Chunk) any { return &x.Snippet }},
				ChunkEmbBits: store.Joined("embedding", "bits", JoinEmbedding, func(x *Chunk) any { return &x.EmbBits }),
				ChunkEmbF32:  store.Joined("embedding", "f32", JoinEmbedding, func(x *Chunk) any { return &x.EmbF32 }),
			}),
			dao.OptionalJoinExpr[*Chunk, ChunkField, ChunkSort, int64](JoinDocument,
				store.InnerJoin("document", "id", "chunk", "doc_id")),
			dao.OptionalJoinExpr[*Chunk, ChunkField, ChunkSort, int64](JoinFTS, dao.FullTextJoin(ChunkFTS)),
			dao.OptionalJoinExpr[*Chunk, ChunkField, ChunkSort, int64](JoinEmbedding,
				store.InnerJoin("embedding", "text_hash", "chunk", "text_hash")),
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
				NameWorkspace: store.Col("doc_name", NameWorkspace, func(n *DocName) any { return &n.WorkspaceID }),
				NameKey:       store.Col("doc_name", NameKey, func(n *DocName) any { return &n.Key }),
				NameDoc:       store.Col("doc_name", NameDoc, func(n *DocName) any { return &n.DocID }),
				NameIsPath:    store.Col("doc_name", NameIsPath, func(n *DocName) any { return &n.IsPath }),
				NameDocPath:   store.Joined("document", "path", JoinDocument, func(n *DocName) any { return &n.DocPath }),
			}),
			dao.OptionalJoinExpr[*DocName, DocNameField, noSort, int64](JoinDocument,
				store.InnerJoin("document", "id", "doc_name", "doc_id"))),
		linkKeys: dao.New[*LinkKey, LinkKeyField, noSort, int64](c,
			dao.Table[*LinkKey, LinkKeyField, noSort, int64]("link_key"),
			dao.Fields[*LinkKey, LinkKeyField, noSort, int64](map[LinkKeyField]dao.Field[*LinkKey]{
				LinkKeyWorkspace: store.Col("link_key", LinkKeyWorkspace, func(k *LinkKey) any { return &k.WorkspaceID }),
				LinkKeyLink:      store.Col("link_key", LinkKeyLink, func(k *LinkKey) any { return &k.LinkID }),
				LinkKeyOrd:       store.Col("link_key", LinkKeyOrd, func(k *LinkKey) any { return &k.Ord }),
				LinkKeyKey:       store.Col("link_key", LinkKeyKey, func(k *LinkKey) any { return &k.Key }),
			})),
		facets: dao.New[*Facet, FacetField, noSort, int64](c,
			dao.Table[*Facet, FacetField, noSort, int64]("doc_facet"),
			dao.Fields[*Facet, FacetField, noSort, int64](map[FacetField]dao.Field[*Facet]{
				FacetWorkspace: store.Col("doc_facet", FacetWorkspace, func(f *Facet) any { return &f.WorkspaceID }),
				FacetDoc:       store.Col("doc_facet", FacetDoc, func(f *Facet) any { return &f.DocID }),
				FacetName:      store.Col("doc_facet", FacetName, func(f *Facet) any { return &f.Field }),
				FacetValue:     store.Col("doc_facet", FacetValue, func(f *Facet) any { return &f.Value }),
			}),
			dao.Conflict[*Facet, FacetField, noSort, int64](FacetWorkspace, FacetName, FacetValue, FacetDoc),
			dao.SortMap[*Facet, FacetField, noSort, int64](map[noSort]string{byKey: `"doc_facet"."field", "doc_facet"."value"`})),
		diagnostics: dao.New[*Diagnostic, DiagnosticField, DiagnosticSort, int64](c,
			dao.Table[*Diagnostic, DiagnosticField, DiagnosticSort, int64]("doc_diagnostic"),
			dao.Fields[*Diagnostic, DiagnosticField, DiagnosticSort, int64](map[DiagnosticField]dao.Field[*Diagnostic]{
				DiagWorkspace: store.Col("doc_diagnostic", DiagWorkspace, func(d *Diagnostic) any { return &d.WorkspaceID }),
				DiagDoc:       store.Col("doc_diagnostic", DiagDoc, func(d *Diagnostic) any { return &d.DocID }),
				DiagOrd:       store.Col("doc_diagnostic", DiagOrd, func(d *Diagnostic) any { return &d.Ord }),
				DiagField:     store.Col("doc_diagnostic", DiagField, func(d *Diagnostic) any { return &d.Field }),
				DiagLine:      store.Col("doc_diagnostic", DiagLine, func(d *Diagnostic) any { return &d.Line }),
				DiagRule:      store.Col("doc_diagnostic", DiagRule, func(d *Diagnostic) any { return &d.Rule }),
				DiagMessage:   store.Col("doc_diagnostic", DiagMessage, func(d *Diagnostic) any { return &d.Message }),
				DiagDocPath:   store.Joined("document", "path", JoinDocument, func(d *Diagnostic) any { return &d.DocPath }),
			}),
			dao.OptionalJoinExpr[*Diagnostic, DiagnosticField, DiagnosticSort, int64](JoinDocument,
				store.InnerJoin("document", "id", "doc_diagnostic", "doc_id")),
			dao.SortMap[*Diagnostic, DiagnosticField, DiagnosticSort, int64](map[DiagnosticSort]string{
				DiagByPath: `"document"."path", "doc_diagnostic"."ord"`,
			}),
			dao.JoinForSort[*Diagnostic, DiagnosticField, DiagnosticSort, int64](DiagByPath, JoinDocument)),
		linksOut: links(c, "dst_doc", true),
		linksIn:  links(c, "src_doc", false),
		embeddings: dao.New[*Embedding, EmbeddingField, noSort, string](c,
			dao.Table[*Embedding, EmbeddingField, noSort, string]("embedding"),
			dao.Fields[*Embedding, EmbeddingField, noSort, string](map[EmbeddingField]dao.Field[*Embedding]{
				EmbWorkspace: store.Col("embedding", EmbWorkspace, func(e *Embedding) any { return &e.WorkspaceID }),
				EmbTextHash:  store.Col("embedding", EmbTextHash, func(e *Embedding) any { return &e.TextHash }),
				EmbModel:     store.Col("embedding", EmbModel, func(e *Embedding) any { return &e.ModelFP }),
				EmbBits:      store.Col("embedding", EmbBits, func(e *Embedding) any { return &e.Bits }),
				EmbF32:       store.Col("embedding", EmbF32, func(e *Embedding) any { return &e.F32 }),
			}),
			dao.Conflict[*Embedding, EmbeddingField, noSort, string](EmbWorkspace, EmbTextHash, EmbModel),
			// a model's vectors in key order, as embedding_workspace_model_text holds them: the
			// start sweep's cursor (ADR 1791329335 §2.4)
			dao.SortMap[*Embedding, EmbeddingField, noSort, string](map[noSort]string{byKey: `"embedding"."text_hash"`})),
		jobs: dao.New[*Job, JobField, noSort, string](c,
			dao.Table[*Job, JobField, noSort, string]("index_job"),
			dao.Fields[*Job, JobField, noSort, string](map[JobField]dao.Field[*Job]{
				JobWorkspace:  store.Col("index_job", JobWorkspace, func(j *Job) any { return &j.WorkspaceID }),
				JobPath:       store.Col("index_job", JobPath, func(j *Job) any { return &j.Path }),
				JobSeq:        store.Col("index_job", JobSeq, func(j *Job) any { return &j.Seq }),
				JobReason:     store.Col("index_job", JobReason, func(j *Job) any { return &j.Reason }),
				JobEnqueuedAt: store.Col("index_job", JobEnqueuedAt, func(j *Job) any { return &j.EnqueuedAt }),
				JobAttempts:   store.Col("index_job", JobAttempts, func(j *Job) any { return &j.Attempts }),
				JobLastError:  store.Col("index_job", JobLastError, func(j *Job) any { return &j.LastError }),
			}),
			dao.Conflict[*Job, JobField, noSort, string](JobWorkspace, JobPath),
			dao.SortMap[*Job, JobField, noSort, string](map[noSort]string{byPath: `"index_job"."path"`, bySeq: `"index_job"."seq"`})),
		changes: dao.New[*ChangeRow, ChangeField, noSort, int64](c,
			dao.Table[*ChangeRow, ChangeField, noSort, int64]("change"),
			dao.Fields[*ChangeRow, ChangeField, noSort, int64](map[ChangeField]dao.Field[*ChangeRow]{
				ChangeWorkspace:  store.Col("change", ChangeWorkspace, func(x *ChangeRow) any { return &x.WorkspaceID }),
				ChangeSeq:        store.Col("change", ChangeSeq, func(x *ChangeRow) any { return &x.Seq }),
				ChangePath:       store.Col("change", ChangePath, func(x *ChangeRow) any { return &x.Path }),
				ChangeOp:         store.Col("change", ChangeOp, func(x *ChangeRow) any { return &x.Op }),
				ChangeGeneration: store.Col("change", ChangeGeneration, func(x *ChangeRow) any { return &x.Generation }),
				ChangeAt:         store.Col("change", ChangeAt, func(x *ChangeRow) any { return &x.At }),
			}),
			dao.SortMap[*ChangeRow, ChangeField, noSort, int64](map[noSort]string{bySeq: `"change"."seq"`})),
	}
}

// docValues is doc_tag or doc_alias: one declaration, two tables.
func docValues(c dao.DataConn, table, valueCol string) *dao.Schema[*DocValue, DocValueField, noSort, int64] {
	return dao.New[*DocValue, DocValueField, noSort, int64](c,
		dao.Table[*DocValue, DocValueField, noSort, int64](table),
		dao.Fields[*DocValue, DocValueField, noSort, int64](map[DocValueField]dao.Field[*DocValue]{
			DocValueWorkspace: store.Col(table, DocValueWorkspace, func(v *DocValue) any { return &v.WorkspaceID }),
			DocValueDoc:       store.Col(table, DocValueDoc, func(v *DocValue) any { return &v.DocID }),
			DocValueValue:     store.Col(table, valueCol, func(v *DocValue) any { return &v.Value }),
		}),
		dao.Conflict[*DocValue, DocValueField, noSort, int64](DocValueWorkspace, DocValueValue, DocValueDoc),
		dao.SortMap[*DocValue, DocValueField, noSort, int64](map[noSort]string{byKey: `"` + table + `"."` + valueCol + `"`}))
}

// links is the link table joined to the document at one end: dst_doc for the
// targets of a document's links (a LEFT JOIN, since a link may resolve to
// none), src_doc for the sources of its backlinks.
func links(c dao.DataConn, end string, left bool) *dao.Schema[*LinkRow, LinkField, LinkSort, int64] {
	join := store.InnerJoin("document", "id", "link", end)
	if left {
		join = store.LeftJoin("document", "id", "link", end)
	}
	return dao.New[*LinkRow, LinkField, LinkSort, int64](c,
		dao.Table[*LinkRow, LinkField, LinkSort, int64]("link"),
		dao.ID[*LinkRow, LinkField, LinkSort, int64](LinkID),
		dao.Fields[*LinkRow, LinkField, LinkSort, int64](map[LinkField]dao.Field[*LinkRow]{
			LinkID:        store.Col("link", LinkID, func(l *LinkRow) any { return &l.ID }),
			LinkWorkspace: store.Col("link", LinkWorkspace, func(l *LinkRow) any { return &l.WorkspaceID }),
			LinkSrc:       store.Col("link", LinkSrc, func(l *LinkRow) any { return &l.SrcDoc }),
			LinkGenFrom:   store.Col("link", LinkGenFrom, func(l *LinkRow) any { return &l.GenFrom }),
			LinkGenTo:     store.Col("link", LinkGenTo, func(l *LinkRow) any { return &l.GenTo }),
			LinkRaw:       store.Col("link", LinkRaw, func(l *LinkRow) any { return &l.Raw }),
			LinkName:      store.Col("link", LinkName, func(l *LinkRow) any { return &l.Name }),
			LinkDst:       store.Col("link", LinkDst, func(l *LinkRow) any { return &l.DstDoc }),
			LinkAnchor:    store.Col("link", LinkAnchor, func(l *LinkRow) any { return &l.Anchor }),
			LinkKind:      store.Col("link", LinkKind, func(l *LinkRow) any { return &l.Kind }),
			LinkTarget:    store.Col("link", LinkTarget, func(l *LinkRow) any { return &l.Target }),
			LinkOtherPath: {Expr: dao.Coalesce(dao.T("document", "path"), store.NoText), Join: JoinOther, ReadOnly: true,
				Scan: func(l *LinkRow) any { return &l.OtherPath }},
		}),
		dao.OptionalJoinExpr[*LinkRow, LinkField, LinkSort, int64](JoinOther, join),
		dao.SortMap[*LinkRow, LinkField, LinkSort, int64](map[LinkSort]string{
			LinkByID:        `"link"."id"`,
			LinkByOtherPath: `"document"."path", "link"."id"`,
		}),
		dao.JoinForSort[*LinkRow, LinkField, LinkSort, int64](LinkByOtherPath, JoinOther))
}

// The workspace's tables, each scoped to the store's workspace (store.Scoped). linksOut is the
// link table joined to each link's target, linksIn to its source: both are the one link table.
func (s *Store) documents(tx *store.Tx) dao.DAO[*Document, DocumentField, int64] {
	return store.Scoped(s.sc, tables.In(tx).documents, tx)
}
func (s *Store) chunks(tx *store.Tx) dao.DAO[*Chunk, ChunkField, int64] {
	return store.Scoped(s.sc, tables.In(tx).chunks, tx)
}
func (s *Store) tags(tx *store.Tx) dao.DAO[*DocValue, DocValueField, int64] {
	return store.Scoped(s.sc, tables.In(tx).tags, tx)
}
func (s *Store) aliases(tx *store.Tx) dao.DAO[*DocValue, DocValueField, int64] {
	return store.Scoped(s.sc, tables.In(tx).aliases, tx)
}
func (s *Store) names(tx *store.Tx) dao.DAO[*DocName, DocNameField, int64] {
	return store.Scoped(s.sc, tables.In(tx).names, tx)
}
func (s *Store) linkKeys(tx *store.Tx) dao.DAO[*LinkKey, LinkKeyField, int64] {
	return store.Scoped(s.sc, tables.In(tx).linkKeys, tx)
}
func (s *Store) facets(tx *store.Tx) dao.DAO[*Facet, FacetField, int64] {
	return store.Scoped(s.sc, tables.In(tx).facets, tx)
}
func (s *Store) diagnostics(tx *store.Tx) dao.DAO[*Diagnostic, DiagnosticField, int64] {
	return store.Scoped(s.sc, tables.In(tx).diagnostics, tx)
}
func (s *Store) linksOut(tx *store.Tx) dao.DAO[*LinkRow, LinkField, int64] {
	return store.Scoped(s.sc, tables.In(tx).linksOut, tx)
}
func (s *Store) linksIn(tx *store.Tx) dao.DAO[*LinkRow, LinkField, int64] {
	return store.Scoped(s.sc, tables.In(tx).linksIn, tx)
}
func (s *Store) embeddings(tx *store.Tx) dao.DAO[*Embedding, EmbeddingField, string] {
	return store.Scoped(s.sc, tables.In(tx).embeddings, tx)
}
func (s *Store) jobs(tx *store.Tx) dao.DAO[*Job, JobField, string] {
	return store.Scoped(s.sc, tables.In(tx).jobs, tx)
}
func (s *Store) changes(tx *store.Tx) dao.DAO[*ChangeRow, ChangeField, int64] {
	return store.Scoped(s.sc, tables.In(tx).changes, tx)
}

// The batches into them: every row added is stamped with the workspace.
func (s *Store) chunkBatch(tx *store.Tx) store.Batch[*Chunk, ChunkField] {
	return store.NewBatch(s.sc, tables.In(tx).chunks, tx)
}
func (s *Store) tagBatch(tx *store.Tx) store.Batch[*DocValue, DocValueField] {
	return store.NewBatch(s.sc, tables.In(tx).tags, tx)
}
func (s *Store) aliasBatch(tx *store.Tx) store.Batch[*DocValue, DocValueField] {
	return store.NewBatch(s.sc, tables.In(tx).aliases, tx)
}
func (s *Store) nameBatch(tx *store.Tx) store.Batch[*DocName, DocNameField] {
	return store.NewBatch(s.sc, tables.In(tx).names, tx)
}
func (s *Store) linkKeyBatch(tx *store.Tx) store.Batch[*LinkKey, LinkKeyField] {
	return store.NewBatch(s.sc, tables.In(tx).linkKeys, tx)
}
func (s *Store) facetBatch(tx *store.Tx) store.Batch[*Facet, FacetField] {
	return store.NewBatch(s.sc, tables.In(tx).facets, tx)
}
func (s *Store) diagnosticBatch(tx *store.Tx) store.Batch[*Diagnostic, DiagnosticField] {
	return store.NewBatch(s.sc, tables.In(tx).diagnostics, tx)
}
func (s *Store) embeddingBatch(tx *store.Tx) store.Batch[*Embedding, EmbeddingField] {
	return store.NewBatch(s.sc, tables.In(tx).embeddings, tx)
}
