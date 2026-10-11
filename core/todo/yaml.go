package todo

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The frontmatter is a strict subset of YAML, read the way auto-core's tinyyaml reads it:
// mappings, block sequences, scalars, one-line flow sequences and `|`/`>` block scalars. Anchors,
// aliases, merge keys and tags are refused before parsing, a duplicate key is an error, and an
// unquoted date stays a string.

type nodeKind uint8

const (
	nNull nodeKind = iota
	nString
	nBool
	nNumber
	nList
	nMap
)

// node is one decoded YAML value.
type node struct {
	kind  nodeKind
	str   string  // nString; nNumber's text
	num   float64 // nNumber
	b     bool    // nBool
	items []*node // nList
	keys  []string
	vals  []*node // nMap, parallel to keys
}

func (n *node) get(key string) *node {
	for i, k := range n.keys {
		if k == key {
			return n.vals[i]
		}
	}
	return nil
}

// kindName is the type a validation message names, as auto-core's messages name Lua's types.
func (n *node) kindName() string {
	switch n.kind {
	case nString:
		return "string"
	case nBool:
		return "boolean"
	case nNumber:
		return "number"
	case nList, nMap:
		return "table"
	}
	return "nil"
}

// luaSpace is Lua's %s: the characters auto-core's patterns treat as whitespace.
func luaSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r'
}

var (
	// a line that opens a block scalar: `key: |`, `- >-`, `key: |2 # c`
	blockOpener = regexp.MustCompile(`[:-][ \t\n\v\f\r]*[|>][-+]?[0-9]*([ \t\n\v\f\r]*$|[ \t\n\v\f\r]*#)`)
	// the text before a line's value position: indent, a list dash, a key and its colon
	valuePrefix = regexp.MustCompile(`^[ \t\n\v\f\r]*-?[ \t\n\v\f\r]*[A-Za-z0-9_.-]*:?[ \t\n\v\f\r]*`)
	trailingCmt = regexp.MustCompile(`[ \t\n\v\f\r]+#.*$`)
	anchorRe    = regexp.MustCompile(`^&[A-Za-z0-9_-]+`)
	aliasRe     = regexp.MustCompile(`^\*[A-Za-z0-9_-]+`)
	tagRe       = regexp.MustCompile(`^!![A-Za-z0-9_-]+`)
	mergeRe     = regexp.MustCompile(`^[ \t\n\v\f\r]*-?[ \t\n\v\f\r]*<<[ \t\n\v\f\r]*:`)
	inlineMapRe = regexp.MustCompile(`^[^'"\t\n\v\f\r ]*:`)
	intRe       = regexp.MustCompile(`^[+-]?[0-9]+\.?$`)
	floatRe     = regexp.MustCompile(`^[+-]?[0-9]+\.[0-9]+$`)
)

// checkForbidden refuses anchors, aliases, explicit tags and merge keys outside block scalar
// bodies: yaml.lua's check_forbidden, line for line.
func checkForbidden(src string) error {
	lines := strings.Split(src, "\n")
	body := blockBodyLines(lines)
	for n, line := range lines {
		if body[n] {
			continue
		}
		stripped := trailingCmt.ReplaceAllString(line, "")
		value := stripped[len(valuePrefix.FindString(stripped)):]
		if value != "" {
			if m := anchorRe.FindString(value); m != "" {
				return fmt.Errorf("line %d: YAML anchor `%s` not permitted in strict subset", n+1, m)
			}
			if m := aliasRe.FindString(value); m != "" {
				return fmt.Errorf("line %d: YAML alias `%s` not permitted in strict subset", n+1, m)
			}
			if m := tagRe.FindString(value); m != "" {
				return fmt.Errorf("line %d: YAML explicit tag `%s` not permitted in strict subset", n+1, m)
			}
		}
		if mergeRe.MatchString(stripped) {
			return fmt.Errorf("line %d: YAML merge key `<<:` not permitted in strict subset", n+1)
		}
	}
	return nil
}

// blockBodyLines marks the lines inside a block scalar's body: blank lines, and lines indented
// past the line that opened the block.
func blockBodyLines(lines []string) map[int]bool {
	body := map[int]bool{}
	for i := 0; i < len(lines); {
		if !blockOpener.MatchString(lines[i]) {
			i++
			continue
		}
		parent := leadingSpaces(lines[i])
		j := i + 1
		for j < len(lines) && (strings.TrimLeft(lines[j], " \t\n\v\f\r") == "" || leadingSpaces(lines[j]) > parent) {
			body[j] = true
			j++
		}
		i = j
	}
	return body
}

func leadingSpaces(s string) int { return len(s) - len(strings.TrimLeft(s, " ")) }

// decodeYAML parses a frontmatter block into its top-level mapping.
func decodeYAML(src string) (*node, error) {
	if err := checkForbidden(src); err != nil {
		return nil, err
	}
	lines := strings.Split(src, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	p := &parser{lines: lines}
	p.skipBlank()
	if p.i == len(p.lines) {
		return nil, errors.New("frontmatter is empty")
	}
	ind, err := p.indent(p.i)
	if err != nil {
		return nil, err
	}
	if isDash(p.lines[p.i][ind:]) {
		return nil, errors.New("frontmatter is not a mapping")
	}
	m, err := p.mapping(ind)
	if err != nil {
		return nil, err
	}
	p.skipBlank()
	if p.i < len(p.lines) {
		return nil, fmt.Errorf("line %d: bad indentation", p.i+1)
	}
	return m, nil
}

type parser struct {
	lines []string
	i     int
}

// blank reports a line with nothing to parse: whitespace, or a comment.
func blank(line string) bool {
	t := strings.TrimLeft(line, " \t")
	return t == "" || t[0] == '#'
}

func (p *parser) skipBlank() {
	for p.i < len(p.lines) && blank(p.lines[p.i]) {
		p.i++
	}
}

// nextContent is the index of the next non-blank line at or after i, or len(lines).
func (p *parser) nextContent(i int) int {
	for i < len(p.lines) && blank(p.lines[i]) {
		i++
	}
	return i
}

// indent counts a line's leading spaces; a tab in the indentation is refused.
func (p *parser) indent(i int) (int, error) {
	line := p.lines[i]
	n := leadingSpaces(line)
	if n < len(line) && line[n] == '\t' {
		return 0, fmt.Errorf("line %d: tab in indentation", i+1)
	}
	return n, nil
}

func isDash(s string) bool {
	return s == "-" || strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "-\t")
}

func (p *parser) mapping(indent int) (*node, error) {
	m := &node{kind: nMap}
	for {
		p.skipBlank()
		if p.i == len(p.lines) {
			return m, nil
		}
		ind, err := p.indent(p.i)
		if err != nil {
			return nil, err
		}
		if ind < indent {
			return m, nil
		}
		if ind > indent {
			return nil, fmt.Errorf("line %d: bad indentation", p.i+1)
		}
		line := p.lines[p.i]
		rest := line[ind:]
		if isDash(rest) {
			return m, nil // a sequence at the parent's indent ends this mapping
		}
		key, after, err := splitKey(rest)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", p.i+1, err)
		}
		if m.get(key) != nil {
			return nil, fmt.Errorf("line %d: duplicate key %q", p.i+1, key)
		}
		value := strings.TrimLeft(after, " \t")
		var v *node
		if value == "" || value[0] == '#' {
			p.i++
			v, err = p.nested(indent)
		} else {
			n := p.i
			p.i++
			v, err = p.scalar(value, indent, n)
		}
		if err != nil {
			return nil, err
		}
		m.keys = append(m.keys, key)
		m.vals = append(m.vals, v)
	}
}

// nested is the value of a key with nothing after its colon: a sequence (which may sit at the
// key's own indent), a deeper mapping, or null.
func (p *parser) nested(indent int) (*node, error) {
	j := p.nextContent(p.i)
	if j == len(p.lines) {
		return &node{kind: nNull}, nil
	}
	ind, err := p.indent(j)
	if err != nil {
		return nil, err
	}
	switch rest := p.lines[j][ind:]; {
	case isDash(rest) && ind >= indent:
		p.i = j
		return p.sequence(ind)
	case ind > indent:
		p.i = j
		return p.mapping(ind)
	}
	return &node{kind: nNull}, nil
}

func (p *parser) sequence(indent int) (*node, error) {
	seq := &node{kind: nList}
	for {
		p.skipBlank()
		if p.i == len(p.lines) {
			return seq, nil
		}
		ind, err := p.indent(p.i)
		if err != nil {
			return nil, err
		}
		if ind < indent {
			return seq, nil
		}
		if ind > indent {
			return nil, fmt.Errorf("line %d: bad indentation", p.i+1)
		}
		rest := p.lines[p.i][ind:]
		if !isDash(rest) {
			return seq, nil
		}
		item := strings.TrimLeft(rest[1:], " \t")
		col := len(p.lines[p.i]) - len(item)
		var v *node
		switch {
		case item == "" || item[0] == '#':
			p.i++
			j := p.nextContent(p.i)
			v = &node{kind: nNull}
			if j < len(p.lines) {
				ind2, err := p.indent(j)
				if err != nil {
					return nil, err
				}
				if ind2 > ind {
					p.i = j
					if isDash(p.lines[j][ind2:]) {
						v, err = p.sequence(ind2)
					} else {
						v, err = p.mapping(ind2)
					}
					if err != nil {
						return nil, err
					}
				}
			}
		case isDash(item):
			p.lines[p.i] = strings.Repeat(" ", col) + item
			v, err = p.sequence(col)
		case inlineMapRe.MatchString(item):
			// `- key: value` opens a mapping whose keys sit at the item's column
			p.lines[p.i] = strings.Repeat(" ", col) + item
			v, err = p.mapping(col)
		default:
			n := p.i
			p.i++
			v, err = p.scalar(item, ind, n)
		}
		if err != nil {
			return nil, err
		}
		seq.items = append(seq.items, v)
	}
}

// splitKey reads a mapping key, quoted or plain, and returns the text after its colon.
func splitKey(s string) (key, after string, err error) {
	if s[0] == '"' || s[0] == '\'' {
		k, rest, err := quoted(s)
		if err != nil {
			return "", "", err
		}
		rest = strings.TrimLeft(rest, " \t")
		if !strings.HasPrefix(rest, ":") || (len(rest) > 1 && rest[1] != ' ' && rest[1] != '\t') {
			return "", "", errors.New("failed to classify line")
		}
		return k, rest[1:], nil
	}
	for i := 0; i < len(s); i++ {
		if s[i] == ':' && (i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\t') {
			key = strings.TrimRight(s[:i], " \t")
			if key == "" {
				break
			}
			return key, s[i+1:], nil
		}
	}
	return "", "", errors.New("failed to classify line")
}

// scalar reads the value on line n (text, already trimmed of the key or dash). A block scalar
// consumes the lines indented past parent that follow.
func (p *parser) scalar(text string, parent, n int) (*node, error) {
	switch text[0] {
	case '"', '\'':
		s, rest, err := quoted(text)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n+1, err)
		}
		if r := strings.TrimLeft(rest, " \t"); r != "" && r[0] != '#' {
			return nil, fmt.Errorf("line %d: unexpected text after a quoted scalar", n+1)
		}
		return &node{kind: nString, str: s}, nil
	case '[':
		return flowSeq(text, n)
	case '{':
		if r := strings.TrimLeft(text[1:], " \t"); strings.HasPrefix(r, "}") {
			if t := strings.TrimLeft(r[1:], " \t"); t == "" || t[0] == '#' {
				return &node{kind: nMap}, nil
			}
		}
		return nil, fmt.Errorf("line %d: flow mappings are not supported", n+1)
	case '|', '>':
		return p.block(text, parent, n)
	case '&', '*', '!':
		return nil, fmt.Errorf("line %d: anchors, aliases and tags are not supported", n+1)
	}
	s := plain(text)
	if strings.Contains(s, ": ") || strings.HasSuffix(s, ":") {
		return nil, fmt.Errorf("line %d: a mapping value is not allowed here (quote the text)", n+1)
	}
	return typed(s), nil
}

// plain reads an unquoted scalar up to a comment, trimmed.
func plain(s string) string {
	for i := 1; i < len(s); i++ {
		if s[i] == '#' && (s[i-1] == ' ' || s[i-1] == '\t') {
			s = s[:i]
			break
		}
	}
	return strings.TrimRight(s, " \t\n\v\f\r")
}

// typed gives a plain scalar its type the way tinyyaml does: null, booleans, numbers, else text.
func typed(s string) *node {
	switch s {
	case "", "~", "null", "Null", "NULL":
		return &node{kind: nNull}
	case "true", "True", "TRUE":
		return &node{kind: nBool, b: true}
	case "false", "False", "FALSE":
		return &node{kind: nBool}
	case ".inf", ".Inf", ".INF", "+.inf", "+.Inf", "+.INF", "-.inf", "-.Inf", "-.INF", ".nan", ".NaN", ".NAN":
		return &node{kind: nNumber, str: s}
	}
	if intRe.MatchString(s) || floatRe.MatchString(s) {
		f, err := strconv.ParseFloat(strings.TrimSuffix(s, "."), 64)
		if err == nil {
			return &node{kind: nNumber, str: s, num: f}
		}
	}
	return &node{kind: nString, str: s}
}

// quoted reads a single- or double-quoted scalar that closes on its own line.
func quoted(s string) (val, rest string, err error) {
	q := s[0]
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case q == '\'' && c == '\'':
			if i+1 < len(s) && s[i+1] == '\'' {
				b.WriteByte('\'')
				i++
				continue
			}
			return b.String(), s[i+1:], nil
		case q == '"' && c == '"':
			return b.String(), s[i+1:], nil
		case q == '"' && c == '\\' && i+1 < len(s):
			i++
			n, err := unescape(s, &i)
			if err != nil {
				return "", "", err
			}
			b.WriteString(n)
		default:
			b.WriteByte(c)
		}
	}
	return "", "", errors.New("unterminated quoted scalar")
}

var simpleEscapes = map[byte]string{'0': "\x00", 'a': "\a", 'b': "\b", 't': "\t", '\t': "\t", 'n': "\n",
	'v': "\v", 'f': "\f", 'r': "\r", 'e': "\x1b", ' ': " ", '"': "\"", '/': "/", '\\': "\\", 'N': "\u0085",
	'_': " ", 'L': " ", 'P': " "}

// unescape decodes the escape at s[*i] (the character after the backslash), advancing *i past it.
func unescape(s string, i *int) (string, error) {
	c := s[*i]
	if v, ok := simpleEscapes[c]; ok {
		return v, nil
	}
	width := map[byte]int{'x': 2, 'u': 4, 'U': 8}[c]
	if width == 0 {
		return string(c), nil
	}
	if *i+1+width > len(s) {
		return "", errors.New("short escape in a quoted scalar")
	}
	code, err := strconv.ParseUint(s[*i+1:*i+1+width], 16, 32)
	if err != nil {
		return "", errors.New("bad escape in a quoted scalar")
	}
	*i += width
	if c == 'x' {
		return string([]byte{byte(code)}), nil
	}
	if !utf8.ValidRune(rune(code)) {
		return "", errors.New("bad escape in a quoted scalar")
	}
	return string(rune(code)), nil
}

// flowSeq reads a one-line flow sequence. Its items stay strings, as tinyyaml leaves them.
func flowSeq(text string, n int) (*node, error) {
	seq := &node{kind: nList}
	s := strings.TrimLeft(text[1:], " \t")
	for {
		if s == "" {
			return nil, fmt.Errorf("line %d: unterminated flow sequence", n+1)
		}
		if s[0] == ']' {
			if r := strings.TrimLeft(s[1:], " \t"); r != "" && r[0] != '#' {
				return nil, fmt.Errorf("line %d: unexpected text after a flow sequence", n+1)
			}
			return seq, nil
		}
		var item string
		switch s[0] {
		case '"', '\'':
			v, rest, err := quoted(s)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", n+1, err)
			}
			item, s = v, strings.TrimLeft(rest, " \t")
		case '[', '{':
			return nil, fmt.Errorf("line %d: nested flow collections are not supported", n+1)
		default:
			end := strings.IndexAny(s, ",]")
			if end < 0 {
				return nil, fmt.Errorf("line %d: unterminated flow sequence", n+1)
			}
			item, s = strings.TrimRight(s[:end], " \t"), s[end:]
		}
		seq.items = append(seq.items, &node{kind: nString, str: item})
		if strings.HasPrefix(s, ",") {
			s = strings.TrimLeft(s[1:], " \t")
		} else if !strings.HasPrefix(s, "]") {
			return nil, fmt.Errorf("line %d: bad flow sequence", n+1)
		}
	}
}

// block reads a `|` or `>` block scalar whose body is the lines indented past parent.
func (p *parser) block(header string, parent, n int) (*node, error) {
	h := strings.TrimRight(plain(header), " \t")
	style, h := h[0], h[1:]
	chomp := byte(0)
	explicit := 0
	for _, c := range []byte(h) {
		switch {
		case (c == '-' || c == '+') && chomp == 0:
			chomp = c
		case c >= '1' && c <= '9' && explicit == 0:
			explicit = int(c - '0')
		default:
			return nil, fmt.Errorf("line %d: bad block scalar header %q", n+1, header)
		}
	}
	var lines []string
	content := -1
	if explicit > 0 {
		content = parent + explicit
	}
	for p.i < len(p.lines) {
		line := p.lines[p.i]
		if strings.TrimLeft(line, " ") == "" {
			lines = append(lines, "")
			if content >= 0 && len(line) > content {
				lines[len(lines)-1] = line[content:]
			}
			p.i++
			continue
		}
		ind := leadingSpaces(line)
		if ind <= parent {
			break
		}
		if content < 0 {
			content = ind
		}
		if ind < content {
			return nil, fmt.Errorf("line %d: bad indentation in a block scalar", p.i+1)
		}
		lines = append(lines, line[content:])
		p.i++
	}
	trailing := 0
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
		trailing++
	}
	var text string
	if style == '|' {
		text = strings.Join(lines, "\n")
	} else {
		text = fold(lines)
	}
	switch {
	case len(lines) == 0:
		if chomp == '+' {
			text = strings.Repeat("\n", trailing)
		}
	case chomp == '-':
	case chomp == '+':
		text += strings.Repeat("\n", trailing+1)
	default:
		text += "\n"
	}
	return &node{kind: nString, str: text}, nil
}

// fold joins a folded scalar's lines: a line break between two text lines becomes a space, and
// each blank line a newline.
func fold(lines []string) string {
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			switch {
			case l == "":
				b.WriteByte('\n')
				continue
			case lines[i-1] != "":
				b.WriteByte(' ')
			}
		}
		b.WriteString(l)
	}
	return b.String()
}
