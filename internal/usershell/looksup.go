package usershell

import (
	"path/filepath"
	"strings"
	"unicode"
)

// LooksUp is the program a command line names, when s looks it up as it is written: a bare name,
// which s finds on PATH, or an absolute path. ok is false when s resolves the program itself, so
// nobody but s can say whether it is there: a line in s's own syntax (an assignment first, a pipe,
// a variable, quoting), a leading ~ (from the home folder), a relative path (from the folder the
// line runs in), a path on a drive but not from its root, a glob.
func (s Shell) LooksUp(line string) (program string, ok bool) {
	if strings.ContainsAny(line, s.syntax()) {
		return "", false
	}
	fields := strings.Fields(line)
	if len(fields) == 0 || strings.Contains(fields[0], "=") {
		return "", false
	}
	word := fields[0]
	if s.ownWord(word) {
		return "", false
	}
	switch {
	case filepath.IsAbs(word):
		return word, true
	case filepath.VolumeName(word) != "", strings.ContainsAny(word, "~*?[/"), strings.ContainsRune(word, filepath.Separator):
		return "", false
	}
	return word, true
}

// syntax is the characters that make a line s's own syntax rather than a program and its words.
// PowerShell's has no backslash, a separator on Windows; it has braces (a script block) and @
// (splatting), which /bin/sh's has no need of.
func (s Shell) syntax() string {
	switch s.kind {
	case powershell, winps:
		return ";&|$`()<>'\"{}@"
	default:
		return ";&|$`()<>\\'\""
	}
}

// Describe is how s is said to run a line, for a person: "/bin/sh -c", "C:\...\pwsh.exe -NoProfile
// -Command".
func (s Shell) Describe() string {
	program, args := s.Run("")
	return strings.Join(append([]string{program}, args[:len(args)-1]...), " ")
}

// ownWord reports whether s resolves word itself, never on PATH: /bin/sh's builtins and reserved
// words; PowerShell's keywords, its default aliases (5.1's and 7's), and a cmdlet's name, an
// approved verb, a hyphen and a noun (Write-Output; claude-work is no cmdlet: claude is no verb).
// PowerShell's words are matched as it matches them, whatever their case.
func (s Shell) ownWord(word string) bool {
	switch s.kind {
	case powershell, winps:
		w := strings.ToLower(word)
		if powershellWords[w] {
			return true
		}
		verb, noun, ok := strings.Cut(w, "-")
		return ok && powershellVerbs[verb] && noun != "" && strings.IndexFunc(noun, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) < 0
	default:
		return posixWords[word]
	}
}

// set is words as a set.
func set(words string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}

// posixWords are /bin/sh's builtins (POSIX's special and regular ones, and dash's and bash's common
// others) and its reserved words.
var posixWords = set(`. : [ alias bg break builtin cd command continue echo eval exec exit export false fc fg
	getopts hash jobs kill let local printf pwd read readonly return set shift source test times trap true type
	typeset ulimit umask unalias unset wait ! { } case do done elif else esac fi for function if in select then
	time until while`)

// powershellWords are PowerShell's keywords and its default aliases, 5.1's and 7's.
var powershellWords = set(`begin break catch class clean continue data define do dynamicparam else elseif end
	enum exit filter finally for foreach from function hidden if in param process return static switch throw
	trap try until using var while workflow
	? % ac asnp cat cd cfs chdir clc clear clhy cli clp cls clv cnsn compare copy cp cpi cpp curl cvpa dbp del
	diff dir dnsn ebp echo epal epcsv epsn erase etsn exsn fc fhx fl ft fw gal gbp gc gcb gci gcm gcs gdr gerr
	ghy gi gin gjb gl gm gmo gp gps gpv group gsn gsnp gsv gtz gu gv gwmi h history icm iex ihy ii ipal ipcsv
	ipmo ipsn irm ise iwmi iwr kill lp ls man md measure mi mount move mp mv nal ndr ni nmo npssc nsn nv ogv oh
	popd ps pushd pwd r rbp rcjb rcsn rd rdr ren ri rjb rm rmdir rmo rni rnp rp rsn rsnp rujb rv rvpa rwmi sajb
	sal saps sasv sbp sc scb select set shcm si sl sleep sls sort sp spjb spps spsv start stz sujb sv swmi tee
	trcm type wget where wjb write`)

// powershellVerbs are PowerShell's approved verbs (Get-Verb), the first half of a cmdlet's name.
var powershellVerbs = set(`add approve assert backup block build checkpoint clear close compare complete
	compress confirm connect convert convertfrom convertto copy debug deny deploy disable disconnect dismount edit
	enable enter exit expand export find format get grant group hide import initialize install invoke join limit
	lock measure merge mount move new open optimize out ping pop protect publish push read receive redo register
	remove rename repair request reset resize resolve restart restore resume revoke save search select send set
	show skip split start step stop submit suspend switch sync test trace unblock undo uninstall unlock
	unprotect unpublish unregister update use wait watch write`)
