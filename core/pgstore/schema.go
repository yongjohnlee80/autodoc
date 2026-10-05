package pgstore

import (
	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"
)

// The tables, as the migrations create them.
const (
	tDoc   = "rag_document"
	tChunk = "rag_chunk"
	tEmb   = "rag_embedding"
	tLink  = "rag_link"
	tMeta  = "rag_meta"
)

// --- documents -----------------------------------------------------------------

type docRow struct {
	Tenant, Path, Title string
	Version, Indexer    string
	ID, Generation      int64
	Tags, Facets        []string
	Ready               bool
}

type docField string

const (
	dTenant  docField = "tenant"
	dID      docField = "id"
	dPath    docField = "path"
	dTitle   docField = "title"
	dTags    docField = "tags"
	dFacets  docField = "facets"
	dVersion docField = "version"
	dIndexer docField = "indexer"
	dGen     docField = "generation"
	dReady   docField = "semantic_ready"
)

type docSort string

const dByPath docSort = "path"

type docSchema = dao.Schema[*docRow, docField, docSort, int64]

func newDocSchema(conn dao.DataConn) *docSchema {
	type o = dao.Option[*docRow, docField, docSort, int64]
	return dao.New(conn,
		o(dao.Table[*docRow, docField, docSort, int64](tDoc)),
		o(dao.ID[*docRow, docField, docSort, int64](dID)),
		o(dao.Fields[*docRow, docField, docSort, int64](map[docField]dao.Field[*docRow]{
			dTenant:  {Expr: dao.T(tDoc, dTenant), Scan: func(r *docRow) any { return &r.Tenant }, Value: func(r *docRow) any { return r.Tenant }},
			dID:      {Expr: dao.T(tDoc, dID), Scan: func(r *docRow) any { return &r.ID }, ReadOnly: true},
			dPath:    {Expr: dao.T(tDoc, dPath), Scan: func(r *docRow) any { return &r.Path }, Value: func(r *docRow) any { return r.Path }},
			dTitle:   {Expr: dao.T(tDoc, dTitle), Scan: func(r *docRow) any { return &r.Title }, Value: func(r *docRow) any { return r.Title }},
			dTags:    {Expr: dao.T(tDoc, dTags), Scan: func(r *docRow) any { return &r.Tags }, Value: func(r *docRow) any { return r.Tags }},
			dFacets:  {Expr: dao.T(tDoc, dFacets), Scan: func(r *docRow) any { return &r.Facets }, Value: func(r *docRow) any { return r.Facets }},
			dVersion: {Expr: dao.T(tDoc, dVersion), Scan: func(r *docRow) any { return &r.Version }, Value: func(r *docRow) any { return r.Version }},
			dIndexer: {Expr: dao.T(tDoc, dIndexer), Scan: func(r *docRow) any { return &r.Indexer }, Value: func(r *docRow) any { return r.Indexer }},
			dGen:     {Expr: dao.T(tDoc, dGen), Scan: func(r *docRow) any { return &r.Generation }, Value: func(r *docRow) any { return r.Generation }},
			dReady:   {Expr: dao.T(tDoc, dReady), Scan: func(r *docRow) any { return &r.Ready }, Value: func(r *docRow) any { return r.Ready }},
		})),
		o(dao.Default[*docRow, docField, docSort, int64](dID, dPath, dTitle, dTags, dFacets, dVersion, dIndexer, dGen, dReady)),
		o(dao.SortExpr[*docRow, docField, docSort, int64](dByPath, dao.T(tDoc, dPath))),
	)
}

// --- chunks --------------------------------------------------------------------

// chunkRow is a chunk with what a hit presents from its document, joined.
type chunkRow struct {
	Tenant             string
	Doc                int64
	Ord                int
	Breadcrumb, Body   string
	Embed              string // the text a vector of the chunk is made of when it differs
	Title, Tags        string // the document's, on every chunk: lexical ranking weighs them
	ByteStart, ByteEnd int
	TextHash           []byte
	Path               string // joined from the document
	Generation         int64  // joined from the document
	DocReady           bool   // joined from the document
}

// EmbedText is the text a vector of this chunk is made of: its own when it has one, else the
// breadcrumb and body together — the identity its TextHash is defined over.
func (r *chunkRow) EmbedText() string {
	if r.Embed != "" {
		return r.Embed
	}
	return r.Breadcrumb + "\n" + r.Body
}

type chunkField string

const (
	cTenant chunkField  = "tenant"
	cDoc    chunkField  = "doc"
	cOrd    chunkField  = "ord"
	cCrumb  chunkField  = "breadcrumb"
	cBody   chunkField  = "body"
	cEmb    chunkField  = "embed"
	cTitle  chunkField  = "title"
	cTags   chunkField  = "tags"
	cStart  chunkField  = "byte_start"
	cEnd    chunkField  = "byte_end"
	cHash   chunkField  = "text_hash"
	cPath   chunkField  = "doc_path"
	cGen    chunkField  = "doc_generation"
	cReady  chunkField  = "doc_ready"
	cEmbMod chunkField  = "emb_model"
	joinDoc dao.JoinKey = "doc"
	joinEmb dao.JoinKey = "emb"
)

type chunkSort string

const (
	cByRank    chunkSort = "rank"
	cByNearest chunkSort = "nearest"
	cByPath    chunkSort = "path"
	cByOrd     chunkSort = "ord"
)

// tsv is the chunk table's generated tsvector: title, breadcrumb, tags and body in their own
// weight classes, so either can be tuned later without a schema change. The weights are the local
// store's 10 : 5 : 5 : 1 column for column, scaled by a tenth (1.0, 0.5, 0.5, 0.1), and the
// ranking stays an approximation: ts_rank_cd is a cover-density rank, not bm25. rag_english stems
// with Snowball English and removes no stop word, so every query word stays required.
func tsv() dao.FullTextIndex {
	return dao.FullTextIndex{Name: "tsv", Table: tChunk, Columns: []string{"title", "breadcrumb", "tags", "body"}, Classes: []byte("ABCD"), Config: "rag_english"}
}

type chunkSchema = dao.Schema[*chunkRow, chunkField, chunkSort, int64]

func newChunkSchema(conn dao.DataConn) *chunkSchema {
	type o = dao.Option[*chunkRow, chunkField, chunkSort, int64]
	// every join carries the tenant: a document's id is unique, but the key is (tenant, id)
	docJoin := dao.InnerJoinOn(tDoc, dao.On(dao.T(tDoc, "tenant"), dao.T(tChunk, "tenant")), dao.On(dao.T(tDoc, "id"), dao.T(tChunk, "doc")))
	embJoin := dao.InnerJoinOn(tEmb, dao.On(dao.T(tEmb, "tenant"), dao.T(tChunk, "tenant")), dao.On(dao.T(tEmb, "text_hash"), dao.T(tChunk, "text_hash")))
	return dao.New(conn,
		o(dao.Table[*chunkRow, chunkField, chunkSort, int64](tChunk)),
		o(dao.Fields[*chunkRow, chunkField, chunkSort, int64](map[chunkField]dao.Field[*chunkRow]{
			cTenant: {Expr: dao.T(tChunk, cTenant), Scan: func(r *chunkRow) any { return &r.Tenant }, Value: func(r *chunkRow) any { return r.Tenant }},
			cDoc:    {Expr: dao.T(tChunk, cDoc), Scan: func(r *chunkRow) any { return &r.Doc }, Value: func(r *chunkRow) any { return r.Doc }},
			cOrd:    {Expr: dao.T(tChunk, cOrd), Scan: func(r *chunkRow) any { return &r.Ord }, Value: func(r *chunkRow) any { return r.Ord }},
			cCrumb:  {Expr: dao.T(tChunk, cCrumb), Scan: func(r *chunkRow) any { return &r.Breadcrumb }, Value: func(r *chunkRow) any { return r.Breadcrumb }},
			cBody:   {Expr: dao.T(tChunk, cBody), Scan: func(r *chunkRow) any { return &r.Body }, Value: func(r *chunkRow) any { return r.Body }},
			cEmb:    {Expr: dao.T(tChunk, cEmb), Scan: func(r *chunkRow) any { return &r.Embed }, Value: func(r *chunkRow) any { return r.Embed }},
			cTitle:  {Expr: dao.T(tChunk, cTitle), Scan: func(r *chunkRow) any { return &r.Title }, Value: func(r *chunkRow) any { return r.Title }},
			cTags:   {Expr: dao.T(tChunk, cTags), Scan: func(r *chunkRow) any { return &r.Tags }, Value: func(r *chunkRow) any { return r.Tags }},
			cStart:  {Expr: dao.T(tChunk, cStart), Scan: func(r *chunkRow) any { return &r.ByteStart }, Value: func(r *chunkRow) any { return r.ByteStart }},
			cEnd:    {Expr: dao.T(tChunk, cEnd), Scan: func(r *chunkRow) any { return &r.ByteEnd }, Value: func(r *chunkRow) any { return r.ByteEnd }},
			cHash:   {Expr: dao.T(tChunk, cHash), Scan: func(r *chunkRow) any { return &r.TextHash }, Value: func(r *chunkRow) any { return r.TextHash }},
			cPath:   {Expr: dao.T(tDoc, "path"), Scan: func(r *chunkRow) any { return &r.Path }, Join: joinDoc, ReadOnly: true},
			cGen:    {Expr: dao.T(tDoc, "generation"), Scan: func(r *chunkRow) any { return &r.Generation }, Join: joinDoc, ReadOnly: true},
			cReady:  {Expr: dao.T(tDoc, "semantic_ready"), Scan: func(r *chunkRow) any { return &r.DocReady }, Join: joinDoc, ReadOnly: true},
			cEmbMod: {Expr: dao.T(tEmb, "model"), Scan: func(*chunkRow) any { return new(string) }, Join: joinEmb, ReadOnly: true},
		})),
		o(dao.OptionalJoinExpr[*chunkRow, chunkField, chunkSort, int64](joinDoc, docJoin)),
		o(dao.OptionalJoinExpr[*chunkRow, chunkField, chunkSort, int64](joinEmb, embJoin)),
		o(dao.SortParam[*chunkRow, chunkField, chunkSort, int64](cByRank, dao.RankQuery(tsv(), 1.0, 0.5, 0.5, 0.1))),
		o(dao.SortParam[*chunkRow, chunkField, chunkSort, int64](cByNearest, dao.Distance(dao.T(tEmb, "vec"), dao.Cosine))),
		o(dao.SortExpr[*chunkRow, chunkField, chunkSort, int64](cByPath, dao.T(tDoc, "path"))),
		o(dao.SortExpr[*chunkRow, chunkField, chunkSort, int64](cByOrd, dao.T(tChunk, cOrd))),
		o(dao.JoinForSort[*chunkRow, chunkField, chunkSort, int64](cByPath, joinDoc)),
		o(dao.JoinForSort[*chunkRow, chunkField, chunkSort, int64](cByNearest, joinEmb)),
	)
}

// --- embeddings ------------------------------------------------------------------

type embRow struct {
	Tenant, Model string
	TextHash      []byte
	Vec           postgres.Vector
}

type embField string

const (
	eTenant embField = "tenant"
	eModel  embField = "model"
	eHash   embField = "text_hash"
	eVec    embField = "vec"
)

type embSchema = dao.Schema[*embRow, embField, string, int64]

func newEmbSchema(conn dao.DataConn) *embSchema {
	type o = dao.Option[*embRow, embField, string, int64]
	return dao.New(conn,
		o(dao.Table[*embRow, embField, string, int64](tEmb)),
		o(dao.Fields[*embRow, embField, string, int64](map[embField]dao.Field[*embRow]{
			eTenant: {Expr: dao.T(tEmb, eTenant), Scan: func(r *embRow) any { return &r.Tenant }, Value: func(r *embRow) any { return r.Tenant }},
			eModel:  {Expr: dao.T(tEmb, eModel), Scan: func(r *embRow) any { return &r.Model }, Value: func(r *embRow) any { return r.Model }},
			eHash:   {Expr: dao.T(tEmb, eHash), Scan: func(r *embRow) any { return &r.TextHash }, Value: func(r *embRow) any { return r.TextHash }},
			eVec:    {Expr: dao.T(tEmb, eVec), Scan: func(r *embRow) any { return &r.Vec }, Value: func(r *embRow) any { return r.Vec }},
		})),
		o(dao.Conflict[*embRow, embField, string, int64](eTenant, eModel, eHash)),
	)
}

// --- links -------------------------------------------------------------------------

type linkRow struct {
	Tenant, DstPath string
	Src             int64
}

type linkField string

const (
	lTenant linkField = "tenant"
	lSrc    linkField = "src"
	lDst    linkField = "dst_path"
)

type linkSchema = dao.Schema[*linkRow, linkField, string, int64]

func newLinkSchema(conn dao.DataConn) *linkSchema {
	type o = dao.Option[*linkRow, linkField, string, int64]
	return dao.New(conn,
		o(dao.Table[*linkRow, linkField, string, int64](tLink)),
		o(dao.Fields[*linkRow, linkField, string, int64](map[linkField]dao.Field[*linkRow]{
			lTenant: {Expr: dao.T(tLink, lTenant), Scan: func(r *linkRow) any { return &r.Tenant }, Value: func(r *linkRow) any { return r.Tenant }},
			lSrc:    {Expr: dao.T(tLink, lSrc), Scan: func(r *linkRow) any { return &r.Src }, Value: func(r *linkRow) any { return r.Src }},
			lDst:    {Expr: dao.T(tLink, lDst), Scan: func(r *linkRow) any { return &r.DstPath }, Value: func(r *linkRow) any { return r.DstPath }},
		})),
		o(dao.Conflict[*linkRow, linkField, string, int64](lTenant, lSrc, lDst)),
	)
}

// --- meta --------------------------------------------------------------------------

type metaRow struct {
	Tenant, Active, Target string
}

type metaField string

const (
	mTenant metaField = "tenant"
	mActive metaField = "active_model"
	mTarget metaField = "target_model"
)

type metaSchema = dao.Schema[*metaRow, metaField, string, int64]

func newMetaSchema(conn dao.DataConn) *metaSchema {
	type o = dao.Option[*metaRow, metaField, string, int64]
	return dao.New(conn,
		o(dao.Table[*metaRow, metaField, string, int64](tMeta)),
		o(dao.Fields[*metaRow, metaField, string, int64](map[metaField]dao.Field[*metaRow]{
			mTenant: {Expr: dao.T(tMeta, mTenant), Scan: func(r *metaRow) any { return &r.Tenant }, Value: func(r *metaRow) any { return r.Tenant }},
			mActive: {Expr: dao.T(tMeta, mActive), Scan: func(r *metaRow) any { return &r.Active }, Value: func(r *metaRow) any { return r.Active }},
			mTarget: {Expr: dao.T(tMeta, mTarget), Scan: func(r *metaRow) any { return &r.Target }, Value: func(r *metaRow) any { return r.Target }},
		})),
		o(dao.Conflict[*metaRow, metaField, string, int64](mTenant)),
	)
}

// schemas are the entities on one connection.
type schemas struct {
	doc   *docSchema
	chunk *chunkSchema
	emb   *embSchema
	link  *linkSchema
	meta  *metaSchema
}

func newSchemas(conn dao.DataConn) schemas {
	return schemas{doc: newDocSchema(conn), chunk: newChunkSchema(conn), emb: newEmbSchema(conn),
		link: newLinkSchema(conn), meta: newMetaSchema(conn)}
}
