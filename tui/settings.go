package tui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/yongjohnlee80/golib/parse/qml"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/store"
	wsfilter "github.com/yongjohnlee80/autodoc/core/workspace"
)

// A WORKSPACE'S SETTINGS — one dialog, two tabs (ADR 0214). Edit is what the workspace is and which
// files it holds: its name, its root (typed only when it is added), its frontmatter schema, its
// file types and its rules. Advanced is how it is indexed and embedded, and, where the edition
// offers them, its databases: a source its .view files read, and the destination its index is
// kept in. Save sends what changed in one workspace.configure, which saves all of it or none; a
// refusal opens the dialog again as it was typed, the reason on its help line.

// daemonsProvider is the first provider choice: no provider of its own.
const daemonsProvider = "the daemon's (System › AI models)"

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
}

func readConnInfo(v any) *connInfo {
	m := asMap(v)
	if m == nil {
		return nil
	}
	pw, _ := m["has_password"].(bool)
	return &connInfo{engine: str(m, "engine"), host: str(m, "host"), database: str(m, "database"), user: str(m, "user"),
		schema: str(m, "schema"), hasPassword: pw}
}

// where is the connection in one line: the engine, then user@host/database, its schema, and
// whether a password is kept.
func (c *connInfo) where() string {
	if c == nil {
		return "none"
	}
	var b strings.Builder
	b.WriteString(c.engine)
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
		b.WriteString(" · schema " + c.schema)
	}
	if c.hasPassword {
		b.WriteString(" · password kept")
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
	md, txt, yaml                               int // 0 yes, 1 no
	section                                     string
	policy, provider                            int
	dest                                        int
	destDSN, destSchema                         string
	index                                       int
	src                                         int
	srcDSN, srcSchema, viewArgs                 string
}

// settingsBase is what the dialog was opened on: the workspace as the daemon had it, the
// provider choices ("" first, the daemon's), and whether the database settings are offered.
type settingsBase struct {
	w         wsInfo
	adding    bool
	providers []string
	databases bool
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

// form is the dialog filled from the base: what Save would send nothing for.
func (b settingsBase) form() settingsForm {
	w := b.w
	m := wsfilter.NewMatcher(w.include, w.exclude)
	f := settingsForm{
		name: w.name, root: w.root, schema: w.schema.path, texts: strings.Join(w.textExtensions, ", "),
		include: strings.Join(w.include, "; "), exclude: strings.Join(w.exclude, "; "),
		md: typeIndex(m, "md"), txt: typeIndex(m, "txt"), yaml: typeIndex(m, "yaml", "yml"),
		section:  strconv.FormatInt(w.sectionTokens, 10),
		policy:   max(slices.Index(policyChoices, w.embeddingPolicy), 0),
		provider: max(slices.Index(b.providers, w.provider), 0),
		dest:     max(slices.Index(destinationChoices, w.db.destination), 0),
		index:    max(slices.Index(indexChoices, w.db.vectorIndex), 0),
		viewArgs: viewArgsText(w.db.viewArgs),
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
	var texts []string
	for _, t := range strings.FieldsFunc(f.texts, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		texts = append(texts, t)
	}
	if texts, err = kind.TextExtensions(texts); err != nil {
		// as the daemon words it, without the package's prefix
		return nil, errors.New(strings.TrimPrefix(err.Error(), "kind: "))
	}
	if !slices.Equal(texts, w.textExtensions) {
		out["text_extensions"] = anyStrings(texts)
		include, exclude = setTextTypes(include, exclude, w.textExtensions, texts)
	}
	if !slices.Equal(include, w.include) || !slices.Equal(exclude, w.exclude) {
		out["include"], out["exclude"] = anyStrings(include), anyStrings(exclude)
	}

	if s := strings.TrimSpace(f.section); s != opened.section {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 128 || n > 2048 {
			return nil, errors.New("section size must be 128–2048 estimated tokens")
		}
		out["section_tokens"] = n
	}
	if p := pick(policyChoices, f.policy); p != w.embeddingPolicy {
		out["embedding_policy"] = p
	}
	if p := pick(b.providers, f.provider); p != w.provider {
		out["provider"] = p
	}
	if !b.databases {
		return out, nil
	}
	return out, b.databaseChanges(f, out)
}

func (b settingsBase) databaseChanges(f settingsForm, out map[string]any) error {
	db := b.w.db
	dest := pick(destinationChoices, f.dest)
	if dest != db.destination {
		out["destination"] = dest
	}
	index := pick(indexChoices, f.index)
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

	engine, schema := pick(sourceChoices, f.src), strings.TrimSpace(f.srcSchema)
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

func pick(choices []string, i int) string {
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
func changed(c map[string]any) string {
	words := map[string]string{"name": "name", "schema": "schema", "text_extensions": "text types", "include": "rules",
		"section_tokens": "section size", "embedding_policy": "embedding", "provider": "provider",
		"destination": "destination", "destination_connection": "destination", "vector_index": "vector index",
		"source": "source", "view_args": "view args"}
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
		w := wsInfo{name: "untitled", root: homeDir(), include: config.DefaultInclude, exclude: config.DefaultExclude,
			sectionTokens: store.SectionTokensDefault, embeddingPolicy: store.EmbeddingAlways,
			db: databasesInfo{destination: store.DestinationLocal}}
		h.showSettings(settingsBase{w: w, adding: true, providers: providers, databases: databases}, 0, "")
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
		h.notify("choose a workspace before changing its file types")
		return
	}
	h.openSettings(w.name, 0)
}

// openSettings reads the workspace as the daemon has it now (withCurrent), with the provider
// choices and the edition's capabilities, and opens the dialog on it.
func (h *Host) openSettings(name string, tab int) {
	h.withCurrent(name, h.settingsExtras, func(w wsInfo, more any) {
		x, _ := more.(settingsExtras)
		h.showSettings(settingsBase{w: w, providers: withOwnProvider(x.providers, w.provider), databases: x.databases}, tab, "")
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
	ep := h.epoch
	h.dialogSeq++
	seq := h.dialogSeq
	do(h, func(ctx context.Context) answerOf[settingsExtras] {
		x, err := h.settingsExtras(ctx)
		v, _ := x.(settingsExtras)
		return answerOf[settingsExtras]{v: v, err: err}
	}, func(a answerOf[settingsExtras]) {
		switch {
		case ep != h.epoch, seq != h.dialogSeq:
		case a.err != nil:
			h.failed("workspace settings", a.err)
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
	title := "workspace settings · " + w.name
	rootLine := "root: " + w.root + " (a workspace keeps its root: add the other directory as a workspace of its own)"
	if base.adding {
		title, rootLine = "add a workspace", "root: the directory it indexes (Browse… picks one)"
	}
	h.set("App.settingsTitle", title)
	h.set("App.settingsAdding", base.adding)
	h.set("App.settingsRootLine", rootLine)
	h.set("App.databasesShown", base.databases)
	if help == "" {
		help = "Ctrl+PageUp/PageDown switch tabs · Save sends every change at once; a refusal changes nothing"
	}
	h.set("App.settingsHelp", help)
	h.set("App.settingsSchemaState", w.schema.state()+" · e.g. "+suggestedSchema)
	rows := []rowOf{{"key": "", "label": daemonsProvider}}
	for _, n := range base.providers[min(1, len(base.providers)):] {
		rows = append(rows, rowOf{"key": n, "label": n})
	}
	h.wsProviders.Reset(rows)
	providerState := "uses the daemon's provider"
	if w.provider != "" {
		providerState = "uses its own: " + w.provider
		if w.providerErr != "" {
			providerState += " (not set up: " + w.providerErr + "; searching by words)"
		}
	}
	h.set("App.settingsProviderState", providerState)
	h.set("App.settingsDestState", "stored: "+w.db.destConn.where()+" · a blank DSN keeps it")
	h.set("App.settingsSourceState", "stored: "+w.db.source.where()+" · a blank DSN keeps it")
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
		"App.settingsTexts": f.texts, "App.settingsInclude": f.include, "App.settingsExclude": f.exclude,
		"App.settingsSection": f.section, "App.settingsDestDSN": f.destDSN, "App.settingsDestSchema": f.destSchema,
		"App.settingsSrcDSN": f.srcDSN, "App.settingsSrcSchema": f.srcSchema, "App.settingsViewArgs": f.viewArgs} {
		h.setField(k, v)
	}
	for k, v := range map[string]int{"App.settingsMdIndex": f.md, "App.settingsTxtIndex": f.txt, "App.settingsYamlIndex": f.yaml,
		"App.settingsPolicyIndex": f.policy, "App.wsProviderIndex": f.provider, "App.settingsDestIndex": f.dest,
		"App.settingsVectorIndex": f.index, "App.settingsSourceIndex": f.src} {
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

// saveSettings saves the dialog: a new workspace is added, then given what differs from the
// defaults; an existing one is configured with what changed.
func (h *Host) saveSettings(f settingsForm) {
	base := h.settings
	refuse := func(err error) {
		h.showSettings(base, h.settingsTabNow(), "not saved: "+wireMessage(err), f)
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
	if len(c) == 0 {
		h.notify(base.w.name + ": nothing changed")
		return
	}
	name := base.w.name
	do(h, func(ctx context.Context) error {
		_, err := h.call(ctx, "workspace.configure", name, c)
		return err
	}, func(err error) {
		if err != nil {
			refuse(err)
			return
		}
		h.saved(name, c)
	})
}

// addWithSettings adds the workspace, then reads it back as the daemon made it and configures
// what the dialog changed from that. If the add is refused nothing exists; if the configure is,
// the workspace exists with the defaults, and the dialog opens again on it to edit.
func (h *Host) addWithSettings(base settingsBase, f settingsForm, refuse func(error)) {
	name, root := strings.TrimSpace(f.name), strings.TrimSpace(f.root)
	if _, err := base.changes(f); err != nil {
		refuse(err)
		return
	}
	type answer struct {
		added   bool
		w       wsInfo
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
			a.err = err
			return a
		}
		for _, w := range list {
			if w.name == name {
				a.w = w
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
		case !a.added:
			refuse(a.err)
		case a.err != nil:
			h.notify("added workspace " + name + ", but its settings were not saved: " + wireMessage(a.err))
			made := base
			made.w, made.adding = a.w, false
			h.loadWorkspaces()
			h.showSettings(made, h.settingsTabNow(), "added with the defaults; not saved: "+wireMessage(a.err), f)
		default:
			h.notify("added workspace " + name)
			h.loadWorkspaces()
		}
	})
}

// settingsTabNow is the tab the dialog is on, as the dialog last said.
func (h *Host) settingsTabNow() int { return h.settingsTab }

func (h *Host) settingsTabMoved(i int) { h.settingsTab = i }

// saved follows a configure: the workspace in use keeps being used under a new name, a new schema
// checks the open file, and the manager and the explorer are listed again.
func (h *Host) saved(name string, c map[string]any) {
	to := name
	if n, ok := c["name"].(string); ok {
		to = n
		// the list holds the new name at once: a Delete… or Edit… asked for before the relisting
		// answers acts on the workspace as it is now named
		for i := range h.wsList {
			if h.wsList[i].name == name {
				h.wsList[i].name = to
			}
		}
		h.showWorkspaceRows()
		if h.ws == name {
			// the same workspace under another name: the open file stays open
			h.ws = to
			h.setWhere(fmt.Sprintf("autodoc %s · %s", h.session.Version(), to))
			if h.remember != nil {
				h.remember(to)
			}
		}
	}
	h.notify(fmt.Sprintf("%s: saved %s", to, changed(c)))
	h.loadWorkspaces()
	if _, ok := c["schema"]; ok && to == h.ws {
		h.validateSoon()
	}
}

// settingsArgs reads App.saveSettings' arguments, in the dialog's order: name, root, schema, text
// types, include, exclude, the three file types, section, policy, provider, destination, its DSN
// and schema, vector index, source, its DSN and schema, view args.
func settingsArgs(fn func(settingsForm)) func([]qml.SpecValue) error {
	const want = 20
	return func(args []qml.SpecValue) error {
		if len(args) != want {
			return fmt.Errorf("App.saveSettings takes the dialog's %d fields, and was given %d", want, len(args))
		}
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
			dest: n(12), destDSN: s(13), destSchema: s(14), index: n(15), src: n(16), srcDSN: s(17), srcSchema: s(18), viewArgs: s(19)})
		return nil
	}
}

// ---- the manager's right-hand pane ----

// managerDetail is workspace w's settings as the manager shows them beside the list: its Edit
// settings, then its Advanced ones; the databases only where the edition offers them.
func managerDetail(w wsInfo, databases bool) string {
	none := func(s string) string {
		if s == "" {
			return "none"
		}
		return s
	}
	m := wsfilter.NewMatcher(w.include, w.exclude)
	var types []string
	for _, t := range []struct{ label, ext string }{{"Markdown", "md"}, {"plain text", "txt"}, {"YAML", "yaml"}} {
		if m.Match("sample." + t.ext) {
			types = append(types, t.label)
		}
	}
	types = append(types, w.textExtensions...)
	provider := "the daemon's"
	if w.provider != "" {
		provider = w.provider
		if w.providerErr != "" {
			provider += " (not set up: " + w.providerErr + ")"
		}
	}
	lines := []string{
		w.name + " · " + w.state,
		w.root,
		"",
		"EDIT",
		"  file types   " + none(strings.Join(types, ", ")),
		"  include      " + none(strings.Join(w.include, "; ")),
		"  exclude      " + none(strings.Join(w.exclude, "; ")),
		"  schema       " + none(w.schema.path),
		"",
		"ADVANCED",
		fmt.Sprintf("  section      %d estimated tokens", w.sectionTokens),
		"  embedding    " + w.embeddingPolicy,
		"  provider     " + provider,
	}
	if databases {
		dest := "the local store"
		if w.db.destination == store.DestinationPostgres {
			dest = w.db.destConn.where()
			if w.db.vectorIndex != "" {
				dest += " · " + w.db.vectorIndex
			}
		}
		lines = append(lines,
			"  source       "+w.db.source.where(),
			"  view args    "+none(viewArgsText(w.db.viewArgs)),
			"  destination  "+dest)
	}
	return strings.Join(lines, "\n")
}

// managerMoved shows the settings of the manager's row i beside the list.
func (h *Host) managerMoved(i int) {
	h.managerIndex = i
	h.showManagerDetail()
}

func (h *Host) showManagerDetail() {
	if h.managerIndex < 0 || h.managerIndex >= len(h.wsList) {
		h.set("App.managerDetail", "no workspace · Add… makes one")
		return
	}
	h.set("App.managerDetail", managerDetail(h.wsList[h.managerIndex], h.databases))
}
