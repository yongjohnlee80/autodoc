package tui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/yongjohnlee80/golib/parse/qml"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/core/workspace"
	wsfilter "github.com/yongjohnlee80/autodoc/core/workspace"
)

// A WORKSPACE'S SETTINGS — one dialog, two tabs
// (docs/decisions/store.md#workspace-settings-saved-together). Edit is what the workspace is and
// which files it holds: its name, its root (typed only when it is added), its frontmatter schema,
// its file types and its rules. Advanced is how it is indexed and embedded, and, where the edition
// offers them, its databases: a source its .view files read, and the destination its index is kept
// in. Save sends what changed in one workspace.configure, which saves all of it or none; a refusal
// opens the dialog again as it was typed, the reason on its help line.

// The choosers' rows, in the order their indices are read.
var (
	policyChoices      = []string{store.EmbeddingAlways, store.EmbeddingWhenOpened, store.EmbeddingNever}
	destinationChoices = []string{store.DestinationLocal, store.DestinationPostgres}
	destinationLabels  = []string{"the local store", "postgres, with pgvector"}
	indexChoices       = []string{"", store.IndexHNSW, store.IndexIVFFlat}
	indexLabels        = []string{"the default (hnsw)", "hnsw", "ivfflat"}
	sourceChoices      = []string{"", store.EnginePostgres, store.EngineSQLite}
	sourceLabels       = []string{"none", "postgres", "sqlite"}
)

// connInfo is a database connection as workspace.list reports it: where it points, never its DSN.
type connInfo struct {
	engine, host, database, user, schema string
	hasPassword                          bool
	sealed                               bool // its DSN does not open with the store's key: enter it again
}

func readConnInfo(v any) *connInfo {
	m := asMap(v)
	if m == nil {
		return nil
	}
	pw, _ := m["has_password"].(bool)
	sealed, _ := m["sealed"].(bool)
	return &connInfo{engine: str(m, "engine"), host: str(m, "host"), database: str(m, "database"), user: str(m, "user"),
		schema: str(m, "schema"), hasPassword: pw, sealed: sealed}
}

// where is the connection in one line: the engine, then user@host/database, its schema, and
// whether a password is kept.
func (c *connInfo) where(t wording) string {
	if c == nil {
		return t.Tr("autodoc.text.settings.none")
	}
	var b strings.Builder
	b.WriteString(c.engine)
	if c.sealed {
		return c.engine + " · " + t.Tr("autodoc.text.settings.conn_sealed")
	}
	if c.engine == store.EngineSQLite {
		b.WriteString(" · " + c.database)
	} else {
		b.WriteString(" · ")
		if c.user != "" {
			b.WriteString(c.user + "@")
		}
		b.WriteString(c.host + "/" + c.database)
	}
	if c.schema != "" {
		b.WriteString(" · " + t.Trf("autodoc.text.settings.conn_schema", c.schema))
	}
	if c.hasPassword {
		b.WriteString(" · " + t.Tr("autodoc.text.settings.password_kept"))
	}
	return b.String()
}

// databasesInfo is a workspace's database settings as workspace.list reports them.
type databasesInfo struct {
	destination, vectorIndex string
	viewArgs                 map[string]any
	source, destConn         *connInfo
}

func readDatabasesInfo(v any) databasesInfo {
	m := asMap(v)
	d := databasesInfo{destination: str(m, "destination"), vectorIndex: str(m, "vector_index"), viewArgs: asMap(m["view_args"])}
	if d.destination == "" {
		d.destination = store.DestinationLocal
	}
	if s, ok := m["source"]; ok {
		d.source = readConnInfo(s)
	}
	if c, ok := m["destination_connection"]; ok {
		d.destConn = readConnInfo(c)
	}
	return d
}

// viewArgsText is view args as the field shows them: key=value, by key, separated by semicolons.
func viewArgsText(args map[string]any) string {
	keys := slices.Sorted(maps.Keys(args))
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%v", k, args[k])
	}
	return strings.Join(parts, "; ")
}

func parseViewArgs(text string) (map[string]any, error) {
	out := map[string]any{}
	for _, part := range strings.Split(text, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if k = strings.TrimSpace(k); !ok || k == "" {
			return nil, fmt.Errorf("view args are key=value pairs separated by semicolons, not %q", part)
		}
		out[k] = strings.TrimSpace(v)
	}
	return out, nil
}

// settingsForm is the dialog as typed: each field's text, each chooser's index.
type settingsForm struct {
	name, root, schema, texts, include, exclude string
	code                                        string // the daemon's registered types turned on
	docs                                        string // the daemon's derived formats turned on
	md, txt, yaml                               int    // 0 yes, 1 no
	section                                     string
	policy, provider                            int
	dest                                        int
	destDSN, destSchema                         string
	index                                       int
	src                                         int
	srcDSN, srcSchema, viewArgs                 string
	graphOnly                                   string // the graph_only patterns, semicolon-separated
	todoSearch                                  int    // 0 yes, 1 no: the todo tasks in search
}

// settingsBase is what the dialog was opened on: the workspace as the daemon had it, the
// provider choices ("" first, the daemon's), and whether the database settings are offered.
type settingsBase struct {
	w         wsInfo
	adding    bool
	providers []string
	databases bool
	// registered are the extensions the daemon's chunkers read
	// (docs/decisions/search.md#opt-in-file-types): the Edit tab offers each beside the built-in
	// types
	registered []string
	// derived are the formats the daemon reads as documents (.docx, .html; a Pro build's .pdf): the
	// Edit tab's Documents row (docs/decisions/search.md#opt-in-file-types)
	derived []string
}

// typeIndex is a file type's chooser as the rules have it: 0 when a file of each extension at the
// root would be indexed.
func typeIndex(m wsfilter.Matcher, exts ...string) int {
	for _, e := range exts {
		if !m.Match("sample." + e) {
			return 1
		}
	}
	return 0
}

// asText are the stored text extensions still read as text, and those now read as derived
// documents instead (.html, once a text type, is an HTML document): the Edit tab offers only the
// first, and says why the others went (docs/decisions/search.md#stored-html-text-types).
func asText(stored []string) (text, derived []string) {
	for _, e := range stored {
		if slices.Contains(kind.DerivedExtensions, e) {
			derived = append(derived, e)
		} else {
			text = append(text, e)
		}
	}
	return text, derived
}

// textsNote says which stored text types the Edit tab dropped, and why; "" for none. It is in t's
// language.
func textsNote(t wording, derived []string) string {
	if len(derived) == 0 {
		return ""
	}
	return t.Trf("autodoc.text.settings.now_read_as_documents_not_text_dropped_from_your", strings.Join(derived, ", "))
}

// form is the dialog filled from the base: what Save would send nothing for.
func (b settingsBase) form() settingsForm {
	w := b.w
	m := wsfilter.NewMatcher(w.include, w.exclude)
	text, _ := asText(w.textExtensions)
	f := settingsForm{
		name: w.name, root: w.root, schema: w.schema.path, texts: strings.Join(text, ", "),
		include: strings.Join(w.include, "; "), exclude: strings.Join(w.exclude, "; "),
		md: typeIndex(m, "md"), txt: typeIndex(m, "txt"), yaml: typeIndex(m, "yaml", "yml"),
		code:       strings.Join(registeredOn(m, b.registered), ", "),
		docs:       strings.Join(registeredOn(m, b.derived), ", "),
		section:    strconv.FormatInt(w.sectionTokens, 10),
		policy:     max(slices.Index(policyChoices, w.embeddingPolicy), 0),
		provider:   max(slices.Index(b.providers, w.provider), 0),
		dest:       max(slices.Index(destinationChoices, w.db.destination), 0),
		index:      max(slices.Index(indexChoices, w.db.vectorIndex), 0),
		viewArgs:   viewArgsText(w.db.viewArgs),
		graphOnly:  strings.Join(w.graphOnly, "; "),
		todoSearch: yesNo(w.todoSearch),
	}
	if c := w.db.destConn; c != nil {
		f.destSchema = c.schema
	}
	if c := w.db.source; c != nil {
		f.src = max(slices.Index(sourceChoices, c.engine), 0)
		f.srcSchema = c.schema
	}
	return f
}

// setFileTypes applies the file types whose chooser changed from what the rules said: a type
// turned on is included and no longer excluded, one turned off the reverse.
func setFileTypes(include, exclude []string, from, to settingsForm) ([]string, []string) {
	for _, t := range []struct {
		from, to int
		exts     []string
	}{{from.md, to.md, []string{"md"}}, {from.txt, to.txt, []string{"txt"}}, {from.yaml, to.yaml, []string{"yaml", "yml"}}} {
		if t.from == t.to {
			continue
		}
		for _, ext := range t.exts {
			pattern := "**/*." + ext
			on, off := &include, &exclude
			if t.to != 0 {
				on, off = off, on
			}
			*off = slices.DeleteFunc(*off, func(p string) bool { return p == pattern })
			if !slices.Contains(*on, pattern) {
				*on = append(*on, pattern)
			}
		}
	}
	return include, exclude
}

// registeredOn are the registered extensions the rules admit.
func registeredOn(m wsfilter.Matcher, registered []string) []string {
	var on []string
	for _, e := range registered {
		if m.Match("sample" + e) {
			on = append(on, e)
		}
	}
	return on
}

// codeExcludes are what turning a registered type on writes to the excludes, where they are not
// already there: the directories a code repository vendors or builds into. They are written, so
// they are visible and editable, and never applied by condition
// (docs/decisions/search.md#opt-in-file-types).
var codeExcludes = []string{"vendor/**", "target/**", "dist/**", "build/**"}

// setCodeTypes applies the registered types whose state changed: one turned on is included, no
// longer excluded, and brings codeExcludes; one turned off loses its include.
func setCodeTypes(include, exclude, was, now []string) ([]string, []string) {
	return setTypes(include, exclude, was, now, codeExcludes)
}

// setDocTypes applies the document formats whose state changed, as setCodeTypes does but with no
// excludes: a document is not a code repository's build output.
func setDocTypes(include, exclude, was, now []string) ([]string, []string) {
	return setTypes(include, exclude, was, now, nil)
}

// setTypes applies the types whose state changed: one turned on is included, no longer excluded,
// and brings extra to the excludes; one turned off loses its include.
func setTypes(include, exclude, was, now, extra []string) ([]string, []string) {
	for _, e := range now {
		if slices.Contains(was, e) {
			continue
		}
		pattern := "**/*" + e
		exclude = slices.DeleteFunc(exclude, func(p string) bool { return p == pattern })
		if !slices.Contains(include, pattern) {
			include = append(include, pattern)
		}
		for _, x := range extra {
			if !slices.Contains(exclude, x) {
				exclude = append(exclude, x)
			}
		}
	}
	for _, e := range was {
		if pattern := "**/*" + e; !slices.Contains(now, e) {
			include = slices.DeleteFunc(include, func(p string) bool { return p == pattern })
		}
	}
	return include, exclude
}

// setTextTypes admits each new text type with an include, unless the rules already admit it, and
// drops the include of each one taken away.
func setTextTypes(include, exclude, was, now []string) ([]string, []string) {
	m := wsfilter.NewMatcher(include, exclude)
	for _, e := range now {
		if slices.Contains(was, e) {
			continue
		}
		pattern := "**/*" + e
		exclude = slices.DeleteFunc(exclude, func(p string) bool { return p == pattern })
		if !m.Match("sample"+e) && !slices.Contains(include, pattern) {
			include = append(include, pattern)
		}
	}
	for _, e := range was {
		if pattern := "**/*" + e; !slices.Contains(now, e) {
			include = slices.DeleteFunc(include, func(p string) bool { return p == pattern })
		}
	}
	return include, exclude
}

// changes are workspace.configure's settings for f: only what differs from the base, so a save
// that renames restarts nothing, and one that changes nothing sends nothing. The name of a
// workspace being added is workspace.add's, not a change.
func (b settingsBase) changes(f settingsForm) (map[string]any, error) {
	w, opened := b.w, b.form()
	out := map[string]any{}
	if name := strings.TrimSpace(f.name); !b.adding && name != w.name {
		out["name"] = name
	}
	if s := strings.TrimSpace(f.schema); s != w.schema.path {
		out["schema"] = s
	}

	include, err := splitPatternRules(f.include)
	if err != nil {
		return nil, fmt.Errorf("include: %w", err)
	}
	exclude, err := splitPatternRules(f.exclude)
	if err != nil {
		return nil, fmt.Errorf("exclude: %w", err)
	}
	include, exclude = setFileTypes(include, exclude, opened, f)
	texts := strings.FieldsFunc(f.texts, func(r rune) bool { return r == ',' || r == ' ' || r == ';' })
	if texts, err = kind.TextExtensions(texts); err != nil {
		// as the daemon words it, without the package's prefix
		return nil, errors.New(strings.TrimPrefix(err.Error(), "kind: "))
	}
	if !slices.Equal(texts, w.textExtensions) {
		out["text_extensions"] = anyStrings(texts)
		// a derived format dropped from the text types keeps its include: it is still indexed,
		// as a document
		was, _ := asText(w.textExtensions)
		include, exclude = setTextTypes(include, exclude, was, texts)
	}
	code, err := b.codeTypes(f.code)
	if err != nil {
		return nil, err
	}
	if was := registeredOn(wsfilter.NewMatcher(w.include, w.exclude), b.registered); !slices.Equal(was, code) {
		include, exclude = setCodeTypes(include, exclude, was, code)
	}
	docs, err := offeredTypes("documents", f.docs, b.derived)
	if err != nil {
		return nil, err
	}
	if was := registeredOn(wsfilter.NewMatcher(w.include, w.exclude), b.derived); !slices.Equal(was, docs) {
		include, exclude = setDocTypes(include, exclude, was, docs)
	}
	if !slices.Equal(include, w.include) || !slices.Equal(exclude, w.exclude) {
		out["include"], out["exclude"] = anyStrings(include), anyStrings(exclude)
	}

	graphOnly, err := splitPatternRules(f.graphOnly)
	if err != nil {
		return nil, fmt.Errorf("graph only: %w", err)
	}
	if !slices.Equal(graphOnly, w.graphOnly) {
		out["graph_only"] = anyStrings(graphOnly)
	}
	if search := f.todoSearch == 0; search != w.todoSearch && f.todoSearch >= 0 {
		out["todo_search"] = search
	}

	if s := strings.TrimSpace(f.section); s != opened.section {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 128 || n > 2048 {
			return nil, errors.New("section size must be 128–2048 estimated tokens")
		}
		out["section_tokens"] = n
	}
	if p := choiceAt(policyChoices, f.policy); p != w.embeddingPolicy {
		out["embedding_policy"] = p
	}
	if p := choiceAt(b.providers, f.provider); p != w.provider {
		out["provider"] = p
	}
	if !b.databases {
		return out, nil
	}
	return out, b.databaseChanges(f, out)
}

// yesNo is a yes/no chooser's index for b: 0 yes, 1 no.
func yesNo(b bool) int {
	if b {
		return 0
	}
	return 1
}

// codeTypes reads the registered types typed, each one the daemon's.
func (b settingsBase) codeTypes(typed string) ([]string, error) {
	return offeredTypes("code types", typed, b.registered)
}

// offeredTypes reads the extensions typed in a row named what, each one of offered.
func offeredTypes(what, typed string, offered []string) ([]string, error) {
	var out []string
	for _, t := range strings.FieldsFunc(typed, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		e := strings.ToLower(t)
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		if !slices.Contains(offered, e) {
			return nil, fmt.Errorf("%s: this backend reads no %s; it reads %s", what, e, orNone(strings.Join(offered, ", ")))
		}
		if !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	return out, nil
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func (b settingsBase) databaseChanges(f settingsForm, out map[string]any) error {
	db := b.w.db
	dest := choiceAt(destinationChoices, f.dest)
	if dest != db.destination {
		out["destination"] = dest
	}
	index := choiceAt(indexChoices, f.index)
	switch dest {
	case store.DestinationPostgres:
		schema := strings.TrimSpace(f.destSchema)
		if db.destConn == nil || f.destDSN != "" || schema != db.destConn.schema {
			out["destination_connection"] = map[string]any{"engine": store.EnginePostgres, "dsn": f.destDSN, "schema": schema}
		}
	default:
		// the local store has no connection and no vector index to choose
		index = ""
		if db.destConn != nil {
			out["destination_connection"] = map[string]any{"remove": true}
		}
	}
	if index != db.vectorIndex {
		out["vector_index"] = index
	}

	engine, schema := choiceAt(sourceChoices, f.src), strings.TrimSpace(f.srcSchema)
	switch {
	case engine == "" && db.source != nil:
		out["source"] = map[string]any{"remove": true}
	case engine != "" && db.source != nil && engine != db.source.engine && f.srcDSN == "":
		return fmt.Errorf("a %s source needs its connection: the stored one is %s's", engine, db.source.engine)
	case engine != "" && (db.source == nil || engine != db.source.engine || f.srcDSN != "" || schema != db.source.schema):
		out["source"] = map[string]any{"engine": engine, "dsn": f.srcDSN, "schema": schema}
	}
	args, err := parseViewArgs(f.viewArgs)
	if err != nil {
		return err
	}
	if !maps.EqualFunc(args, db.viewArgs, func(a, b any) bool { return fmt.Sprint(a) == fmt.Sprint(b) }) {
		out["view_args"] = args
	}
	return nil
}

// choiceAt is choice i, "" when there is none.
func choiceAt(choices []string, i int) string {
	if i < 0 || i >= len(choices) {
		return ""
	}
	return choices[i]
}

func anyStrings(list []string) []any {
	out := make([]any, len(list))
	for i, s := range list {
		out[i] = s
	}
	return out
}

// changed names what a save changed, for its notice.
func changed(t wording, c map[string]any) string {
	destination := t.Tr("autodoc.notice.settings.destination")
	words := map[string]string{"name": t.Tr("autodoc.notice.settings.name"), "schema": t.Tr("autodoc.notice.settings.schema"),
		"text_extensions": t.Tr("autodoc.notice.settings.text_types"), "include": t.Tr("autodoc.notice.settings.rules"),
		"section_tokens": t.Tr("autodoc.notice.settings.section_size"), "embedding_policy": t.Tr("autodoc.notice.settings.embedding"),
		"provider": t.Tr("autodoc.notice.settings.provider"), "destination": destination, "destination_connection": destination,
		"vector_index": t.Tr("autodoc.notice.settings.vector_index"), "source": t.Tr("autodoc.notice.settings.source"),
		"view_args": t.Tr("autodoc.notice.settings.view_args")}
	var out []string
	for k := range c {
		if w, ok := words[k]; ok && !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// ---- the dialog ----

// startAddWorkspace opens the settings to add a workspace: untitled, rooted at the home directory
// until another is typed or browsed to, with the default rules.
func (h *Host) startAddWorkspace() {
	h.withSettingsExtras(func(providers []string, databases bool) {
		w := wsInfo{name: "untitled", root: homeDir(), include: workspace.DefaultInclude(), exclude: workspace.DefaultExclude(),
			sectionTokens: store.SectionTokensDefault, embeddingPolicy: store.EmbeddingAlways,
			db: databasesInfo{destination: store.DestinationLocal}}
		h.dropKBType() // a fresh dialog: no type staged, and none still staging may land
		h.kbTypes.Reset(h.kbTypeRows())
		h.kbTypeKey = ""
		h.loadKBTypes() // the registry's KB types, once fetched (or the cache, or the built-ins)
		h.set("App.kbTypeIndex", -1)
		h.set("App.kbTypeIndex", 0)
		h.set("App.kbTypeSummary", h.noneNoteText())
		h.set("App.kbTypeCustomShown", false)
		h.showSettings(settingsBase{w: w, adding: true, providers: providers, databases: databases, registered: h.kinds.Chunked, derived: h.kinds.Derived}, 0, "")
	})
}

// startEditWorkspace and startAdvancedWorkspace open the manager's row i on a tab.
func (h *Host) startEditWorkspace(i int)     { h.startSettings(i, 0) }
func (h *Host) startAdvancedWorkspace(i int) { h.startSettings(i, 1) }

func (h *Host) startSettings(i, tab int) {
	if w, ok := h.managerRow(i); ok {
		h.openSettings(w.name, tab)
	}
}

// openActiveSettings opens the workspace in use on its Edit tab: Preferences › File types….
func (h *Host) openActiveSettings() {
	w, ok := h.activeWorkspaceInfo()
	if !ok {
		h.notify(h.tr("autodoc.notice.settings.choose_a_workspace_before_changing_its_file_type"))
		return
	}
	h.openSettings(w.name, 0)
}

// openSettings reads the workspace as the daemon has it now (withCurrent), with the provider
// choices and the edition's capabilities, and opens the dialog on it.
func (h *Host) openSettings(name string, tab int) {
	h.withCurrent(name, h.settingsExtras, func(w wsInfo, more any) {
		x, _ := more.(settingsExtras)
		h.showSettings(settingsBase{w: w, providers: withOwnProvider(x.providers, w.provider), databases: x.databases, registered: h.kinds.Chunked, derived: h.kinds.Derived}, tab, "")
	})
}

// withOwnProvider is the provider choices with the workspace's own among them: one gone from the
// stored providers is still its choice, so a save of something else does not take it away.
func withOwnProvider(providers []string, own string) []string {
	if slices.Contains(providers, own) {
		return providers
	}
	return append(slices.Clip(providers), own)
}

type settingsExtras struct {
	providers []string
	databases bool
}

// settingsExtras are the provider choices, "" first, and whether the edition offers the database
// settings; from a worker. A daemon without sys.capabilities offers none.
func (h *Host) settingsExtras(ctx context.Context) (any, error) {
	res, err := h.call(ctx, "embedding.providers")
	if err != nil {
		return nil, err
	}
	x := settingsExtras{providers: []string{""}}
	for _, p := range asList(asMap(res)["providers"]) {
		x.providers = append(x.providers, str(asMap(p), "name"))
	}
	if caps, err := h.call(ctx, "sys.capabilities"); err == nil {
		x.databases, _ = asMap(caps)["databases"].(bool)
	}
	return x, nil
}

// withSettingsExtras is settingsExtras for a dialog with no workspace yet.
func (h *Host) withSettingsExtras(fn func(providers []string, databases bool)) {
	tk := h.ticket(&h.dialogSeq, TicketWorkspace)
	do(h, func(ctx context.Context) answerOf[settingsExtras] {
		x, err := h.settingsExtras(ctx)
		v, _ := x.(settingsExtras)
		return answerOf[settingsExtras]{v: v, err: err}
	}, func(a answerOf[settingsExtras]) {
		switch {
		case !tk.Current():
		case a.err != nil:
			h.failed(h.tr("autodoc.notice.settings.workspace_settings"), a.err)
		default:
			fn(a.v.providers, a.v.databases)
		}
	})
}

// showSettings fills the dialog from base and opens it on tab; typed, when set, is what a refused
// save had in it, shown again with the reason in help.
func (h *Host) showSettings(base settingsBase, tab int, help string, typed ...settingsForm) {
	h.settings = base
	f := base.form()
	if len(typed) > 0 {
		f = typed[0]
	}
	w := base.w
	title := h.trf("autodoc.text.settings.workspace_settings", w.name)
	rootLine := h.trf("autodoc.text.settings.root_a_workspace_keeps_its_root_add_the_other_di", w.root)
	if base.adding {
		title, rootLine = h.tr("autodoc.text.settings.add_a_workspace"), h.tr("autodoc.text.settings.root_the_directory_it_indexes_browse_picks_one")
	}
	h.set("App.settingsTitle", title)
	h.set("App.settingsAdding", base.adding)
	if !base.adding {
		h.set("App.kbTypeCustomShown", false) // the custom URL row belongs to the add dialog alone
	}
	h.set("App.settingsRootLine", rootLine)
	h.set("App.databasesShown", base.databases)
	if help == "" {
		help = h.tr("autodoc.text.settings.ctrl_pageup_pagedown_switch_tabs_save_sends_ever")
	}
	h.set("App.settingsHelp", help)
	h.set("App.settingsSchemaState", h.trf("autodoc.text.settings.e_g", w.schema.state(core{h}), suggestedSchema))
	// the first provider choice: none of its own, the daemon's
	rows := []rowOf{{"key": "", "label": h.tr("autodoc.text.settings.the_daemon_s_system_search_models")}}
	for _, n := range base.providers[min(1, len(base.providers)):] {
		rows = append(rows, rowOf{"key": n, "label": n})
	}
	h.wsProviders.Reset(rows)
	providerState := h.tr("autodoc.text.settings.uses_the_daemon_s_provider")
	if w.provider != "" {
		providerState = h.trf("autodoc.text.settings.uses_its_own", w.provider)
		if w.providerErr != "" {
			providerState = h.trf("autodoc.text.settings.uses_its_own_not_set_up_searching_by_words", w.provider, w.providerErr)
		}
	}
	h.set("App.settingsProviderState", providerState)
	_, dropped := asText(w.textExtensions)
	h.set("App.settingsTextsNote", textsNote(core{h}, dropped))
	h.set("App.settingsTextsNoted", len(dropped) > 0)
	h.set("App.settingsDocsOffered", len(base.derived) > 0)
	h.set("App.settingsDocsLabel", h.trf("autodoc.text.settings.documents", strings.Join(base.derived, ", ")))
	h.set("App.settingsCodeOffered", len(base.registered) > 0)
	// short, so the files frame fits: the excludes code brings are written where the user sees
	// them, in the exclude field
	h.set("App.settingsCodeLabel", h.trf("autodoc.text.settings.code", strings.Join(base.registered, ", ")))
	h.set("App.settingsTodoNote", todoExcludedNote(core{h}, w))
	h.set("App.settingsTodoNoted", len(w.todoExcluded) > 0)
	h.set("App.settingsDestState", h.trf("autodoc.text.settings.stored_a_blank_dsn_keeps_it", w.db.destConn.where(core{h})))
	h.set("App.settingsSourceState", h.trf("autodoc.text.settings.stored_a_blank_dsn_keeps_it", w.db.source.where(core{h})))
	h.fillSettings(f)
	// the view says which tab it moves to only once it is on screen: the tab opened on is known here
	h.settingsTab = tab
	h.set("App.settingsTab", -1) // a source set to what it was is no change: move it, then set it
	h.set("App.settingsTab", tab)
	h.open("workspaceSettings")
}

// fillSettings puts f in the dialog's fields and choosers.
func (h *Host) fillSettings(f settingsForm) {
	for k, v := range map[string]string{"App.settingsName": f.name, "App.settingsRoot": f.root, "App.settingsSchema": f.schema,
		"App.settingsTexts": f.texts, "App.settingsCode": f.code, "App.settingsDocs": f.docs, "App.settingsInclude": f.include, "App.settingsExclude": f.exclude,
		"App.settingsSection": f.section, "App.settingsDestDSN": f.destDSN, "App.settingsDestSchema": f.destSchema,
		"App.settingsSrcDSN": f.srcDSN, "App.settingsSrcSchema": f.srcSchema, "App.settingsViewArgs": f.viewArgs,
		"App.settingsGraphOnly": f.graphOnly} {
		h.setField(k, v)
	}
	for k, v := range map[string]int{"App.settingsMdIndex": f.md, "App.settingsTxtIndex": f.txt, "App.settingsYamlIndex": f.yaml,
		"App.settingsPolicyIndex": f.policy, "App.wsProviderIndex": f.provider, "App.settingsDestIndex": f.dest,
		"App.settingsVectorIndex": f.index, "App.settingsSourceIndex": f.src, "App.settingsTodoSearchIndex": f.todoSearch} {
		h.set(k, -1)
		h.set(k, v)
	}
}

// browseRoot opens the folder picker over the dialog; the folder chosen becomes the root typed.
func (h *Host) browseRoot(root string) {
	h.set("App.browseFolder", root)
	h.open("workspaceRoot")
}

func (h *Host) rootChosen(folder string) { h.setField("App.settingsRoot", folder) }

// newRootFolder is the add dialog's New Folder: it creates the folder the root field names (a
// leading ~ is the home directory), and its parents, so a new KB can start in an empty folder.
// The TUI and the daemon share the disk, as Browse… does.
func (h *Host) newRootFolder(root string) {
	root = strings.TrimSpace(root)
	if rest, ok := strings.CutPrefix(root, "~"); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
		root = homeDir() + rest
	}
	if !filepath.IsAbs(root) {
		h.notify(h.tr("autodoc.notice.settings.new_folder_type_the_folder_s_full_path_in_the_ro"))
		return
	}
	root = filepath.Clean(root)
	if info, err := os.Stat(root); err == nil {
		if info.IsDir() {
			h.notify(h.trf("autodoc.notice.settings.exists_already", root))
		} else {
			h.notify(h.trf("autodoc.notice.settings.is_a_file", root))
		}
		return
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		h.notify(h.trf("autodoc.notice.settings.new_folder", err.Error()))
		return
	}
	h.setField("App.settingsRoot", root)
	h.notify(h.trf("autodoc.notice.settings.created", root))
}

// saveSettings saves the dialog: a new workspace is added, then given what differs from the
// defaults; an existing one is configured with what changed.
func (h *Host) saveSettings(f settingsForm) {
	base := h.settings
	refuse := func(err error) {
		h.showSettings(base, h.settingsTabNow(), h.trf("autodoc.text.settings.not_saved", wireMessage(err)), f)
	}
	if base.adding {
		h.addWithSettings(base, f, refuse)
		return
	}
	c, err := base.changes(f)
	if err != nil {
		refuse(err)
		return
	}
	name := base.w.name
	resume := h.kbResume
	if resume != nil && resume.ws != name {
		resume = nil // a resume belongs to the workspace it added, and to no other
	}
	if len(c) == 0 && resume == nil {
		h.notify(h.trf("autodoc.notice.settings.nothing_changed", base.w.name))
		return
	}
	do(h, func(ctx context.Context) error {
		if resume != nil {
			if _, err := h.applyKBType(ctx, name, h.kbPlan(resume.s)); err != nil {
				return fmt.Errorf("the KB type's files: %w", err)
			}
		}
		if len(c) == 0 {
			return nil
		}
		_, err := h.call(ctx, "workspace.configure", name, c)
		return err
	}, func(err error) {
		if err != nil {
			refuse(err) // the resume is kept: Save again retries, create-only
			return
		}
		if resume != nil && h.kbResume == resume {
			h.dropKBType()
		}
		h.saved(name, c)
	})
}

// addWithSettings adds the workspace, then reads it back as the daemon made it and configures
// what the dialog changed from that. If the add is refused nothing exists; if the configure is,
// the workspace exists with the defaults, and the dialog opens again on it to edit.
func (h *Host) addWithSettings(base settingsBase, f settingsForm, refuse func(error)) {
	name, root := strings.TrimSpace(f.name), strings.TrimSpace(f.root)
	// the type as staged when Add is pressed: a later pick doesn't touch this add
	// (docs/decisions/contract.md#choosing-a-type-stages-it)
	kb := h.kbStaged
	if kb != nil {
		f = base.withKBType(f, kb.t)
	}
	if _, err := base.changes(f); err != nil {
		refuse(err)
		return
	}
	type answer struct {
		added   bool
		unread  bool // added, but not read back: no dialog on a zero wsInfo
		w       wsInfo
		applied kbApply
		partial bool // the KB type stopped part way: what landed is in applied
		changes map[string]any
		err     error
	}
	do(h, func(ctx context.Context) answer {
		if _, err := h.call(ctx, "workspace.add", name, root); err != nil {
			return answer{err: err}
		}
		a := answer{added: true}
		list, err := h.listWorkspaces(ctx)
		if err != nil {
			a.err, a.unread = err, true
			return a
		}
		for _, w := range list {
			if w.name == name {
				a.w = w
			}
		}
		if a.w.name == "" {
			a.err, a.unread = fmt.Errorf("%s is not in the list read back", name), true
			return a
		}
		// the type's files before the configure, so the schema it names exists
		// (docs/decisions/contract.md#the-daemon-writes-the-scaffold-create-only)
		if kb != nil {
			if a.applied, a.err = h.applyKBType(ctx, name, h.kbPlan(kb)); a.err != nil {
				a.partial = true
				return a
			}
		}
		made := base
		made.w, made.adding = a.w, false
		f := f
		f.name = name
		if a.changes, a.err = made.changes(f); a.err == nil && len(a.changes) > 0 {
			_, a.err = h.call(ctx, "workspace.configure", name, a.changes)
		}
		return a
	}, func(a answer) {
		switch {
		case a.added && a.partial:
			h.keepForResume(name, kb) // the reopened dialog's Save finishes it on this workspace
		case a.added:
			h.dropKBType() // the stage's work is done
		}
		made := base
		made.w, made.adding = a.w, false
		switch {
		case !a.added:
			refuse(a.err)
		case a.unread:
			h.failed(h.trf("autodoc.notice.settings.added_workspace_but_could_not_read_it_back", name), a.err)
			h.loadWorkspaces()
		case a.partial:
			applied := a.applied.text(core{h})
			h.failed(h.trf("autodoc.notice.settings.added_workspace_but_its_kb_type_stopped_part_way", name, applied), a.err)
			h.loadWorkspaces()
			h.showSettings(made, h.settingsTabNow(), h.trf("autodoc.text.settings.added_the_kb_type_stopped_part_way_save_finishes", applied, name), f)
		case a.err != nil:
			help := h.trf("autodoc.text.settings.added_with_the_defaults_not_saved", wireMessage(a.err))
			if kb != nil {
				help = h.trf("autodoc.text.settings.scaffolded_not_saved", a.applied.text(core{h}), wireMessage(a.err))
			}
			h.failed(h.trf("autodoc.notice.settings.added_workspace_but_its_settings_were_not_saved", name), a.err)
			h.loadWorkspaces()
			h.showSettings(made, h.settingsTabNow(), help, f)
		default:
			msg := h.trf("autodoc.notice.settings.added_workspace", name)
			if kb != nil {
				msg = h.trf("autodoc.notice.settings.added_workspace_as", name, kb.t.Title, a.applied.text(core{h}))
			}
			h.notify(msg)
			h.loadWorkspaces()
		}
	})
}

// settingsTabNow is the tab the dialog is on, as the dialog last said.
func (h *Host) settingsTabNow() int { return h.settingsTab }

func (h *Host) settingsTabMoved(i int) { h.settingsTab = i }

// renameWorkspaceRow gives the listed workspace name the name to at once, before the relisting
// answers: a Delete… or Edit… asked for meanwhile acts on the workspace as it is now named, and
// wsRefOf finds its root under the new name (an agent's switch, agentswitch.go).
func (h *Host) renameWorkspaceRow(name, to string) {
	list := slices.Clone(h.workspaces.All())
	for i := range list {
		if list[i].name == name {
			list[i].name = to
		}
	}
	h.showWorkspaceRows(list)
}

// saved follows a configure: the workspace in use keeps being used under a new name, a new schema
// checks the open file when it is that file's own workspace's, and the manager and the explorer
// are listed again.
func (h *Host) saved(name string, c map[string]any) {
	to := name
	if n, ok := c["name"].(string); ok {
		to = n
		h.renameWorkspaceRow(name, to)
		if h.ws == name {
			// the same workspace under another name: the open file stays open
			h.ws = to
			h.setWhere(fmt.Sprintf("autodoc %s · %s", h.session.Version(), to))
			if h.remember != nil {
				h.remember(to)
			}
		}
	}
	h.notify(h.trf("autodoc.notice.settings.saved", to, changed(core{h}, c)))
	h.loadWorkspaces()
	if _, ok := c["schema"]; ok && to == h.checkedIn() {
		h.validateSoon(validateDelay)
	}
}

// todoExcludedNote says which exclude patterns keep a todo store of w out of the index
// (docs/decisions/todos.md#excluded-todo-stores-are-reported): its tasks open in no workspace,
// without relations; "" for none. It is in t's language.
func todoExcludedNote(t wording, w wsInfo) string {
	if len(w.todoExcluded) == 0 {
		return ""
	}
	return t.Trf("autodoc.text.settings.exclude_keeps_this_workspace_s_todo_tasks_out_of", strings.Join(w.todoExcluded, "; "))
}

// todoGraphOnly is the notice's button: the exclude patterns that keep a todo store out are taken
// out of the exclude field, so Save indexes the tasks graph-only. Nothing is saved until then.
func (h *Host) todoGraphOnly(exclude string) {
	rules, err := splitPatternRules(exclude)
	if err != nil {
		return
	}
	rules = slices.DeleteFunc(rules, func(p string) bool { return slices.Contains(h.settings.w.todoExcluded, p) })
	h.setField("App.settingsExclude", strings.Join(rules, "; "))
	h.set("App.settingsHelp", h.tr("autodoc.text.settings.the_todo_store_s_exclude_is_out_of_the_field_sav"))
}

// settingsArgs reads App.saveSettings' arguments, in the dialog's order: name, root, schema, text
// types, include, exclude, the three file types, section, policy, provider, destination, its DSN
// and schema, vector index, source, its DSN and schema, view args, the registered types on, the
// document formats on, the graph_only patterns, todo tasks in search.
func settingsArgs(fn func(settingsForm)) Command {
	const want = 24
	return Command{Args: make([]qml.SpecValueKind, want), What: fmt.Sprintf("the dialog's %d fields", want), Run: func(args []qml.SpecValue) error {
		s := func(i int) string { return args[i].Raw }
		n := func(i int) int {
			v, err := strconv.Atoi(args[i].Raw)
			if err != nil {
				return -1
			}
			return v
		}
		fn(settingsForm{name: s(0), root: s(1), schema: s(2), texts: s(3), include: s(4), exclude: s(5),
			md: n(6), txt: n(7), yaml: n(8), section: s(9), policy: n(10), provider: n(11),
			dest: n(12), destDSN: s(13), destSchema: s(14), index: n(15), src: n(16), srcDSN: s(17), srcSchema: s(18), viewArgs: s(19),
			code: s(20), docs: s(21), graphOnly: s(22), todoSearch: n(23)})
		return nil
	}}
}

// ---- the manager's right-hand pane ----

// managerDetail is workspace w's settings as the manager shows them beside the list: its Edit
// settings, then its Advanced ones; the databases only where the edition offers them. It is in t's
// language.
func managerDetail(t wording, w wsInfo, databases bool, registered []string) string {
	none := func(s string) string {
		if s == "" {
			return t.Tr("autodoc.text.settings.none")
		}
		return s
	}
	m := wsfilter.NewMatcher(w.include, w.exclude)
	var types []string
	for _, ty := range []struct{ label, ext string }{{"Markdown", "md"}, {t.Tr("autodoc.text.settings.plain_text"), "txt"}, {"YAML", "yaml"}} {
		if m.Match("sample." + ty.ext) {
			types = append(types, ty.label)
		}
	}
	types = append(types, w.textExtensions...)
	types = append(types, registeredOn(m, registered)...)
	provider := t.Tr("autodoc.text.settings.the_daemon_s")
	if w.provider != "" {
		provider = w.provider
		if w.providerErr != "" {
			provider = t.Trf("autodoc.text.settings.not_set_up", w.provider, w.providerErr)
		}
	}
	todoSearch := t.Tr("autodoc.text.settings.todo_search_off_tasks_are_in_the_graph_only")
	if w.todoSearch {
		todoSearch = t.Tr("autodoc.text.settings.todo_search_on_tasks_are_search_hits")
	}
	// each setting's line is indented under its tab's heading
	lines := []string{
		w.name + " · " + w.state,
		w.root,
		"",
		t.Tr("autodoc.text.settings.edit"),
		"  " + t.Trf("autodoc.text.settings.file_types", none(strings.Join(types, ", "))),
		"  " + t.Trf("autodoc.text.settings.include", none(strings.Join(w.include, "; "))),
		"  " + t.Trf("autodoc.text.settings.exclude", none(strings.Join(w.exclude, "; "))),
		"  " + t.Trf("autodoc.text.settings.schema", none(w.schema.path)),
		"",
		t.Tr("autodoc.text.settings.advanced"),
		"  " + t.Trf("autodoc.text.settings.section_estimated_tokens", w.sectionTokens),
		"  " + t.Trf("autodoc.text.settings.embedding", w.embeddingPolicy),
		"  " + t.Trf("autodoc.text.settings.provider", provider),
		"  " + t.Trf("autodoc.text.settings.graph_only", none(strings.Join(w.graphOnly, "; "))),
		"  " + todoSearch,
	}
	if databases {
		dest := t.Tr("autodoc.text.settings.the_local_store")
		if w.db.destination == store.DestinationPostgres {
			dest = w.db.destConn.where(t)
			if w.db.vectorIndex != "" {
				dest += " · " + w.db.vectorIndex
			}
		}
		lines = append(lines,
			"  "+t.Trf("autodoc.text.settings.source", w.db.source.where(t)),
			"  "+t.Trf("autodoc.text.settings.view_args", none(viewArgsText(w.db.viewArgs))),
			"  "+t.Trf("autodoc.text.settings.destination", dest))
	}
	return strings.Join(lines, "\n")
}

// managerMoved shows the settings of the manager's row i beside the list.
func (h *Host) managerMoved(i int) {
	h.managerIndex = i
	h.showManagerDetail()
}

func (h *Host) showManagerDetail() {
	w, ok := h.managed.At(h.managerIndex)
	if !ok {
		h.set("App.managerDetail", h.tr("autodoc.text.settings.no_workspace_add_makes_one"))
		return
	}
	h.set("App.managerDetail", managerDetail(core{h}, w, h.databases, slices.Concat(h.kinds.Chunked, h.kinds.Derived)))
}
