// Package sshconf reads and writes OpenSSH client configuration files
// (~/.ssh/config).
//
// It parses the file's Host blocks into the aliases a user can connect to,
// resolves the options that apply to each alias the way ssh does (the first
// value of a key wins, across every block matching the alias), and edits the
// file surgically: only the lines of the block being added, rewritten or
// removed change, and every other byte of the file stays as it was.
//
// Blocks whose patterns are not literal (`Host *`, `Host *.example.com`) are
// left out of the entries: they hold defaults for hosts named elsewhere, and
// tern-ssh neither lists nor edits them. Include directives are not followed;
// only the file itself is read and written.
package sshconf

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Marker tags a block written by tern-ssh, so an edit or a removal can name
// the lines it owns.
const Marker = "# tern-ssh"

// Option is one directive of a Host block, as written in the file.
type Option struct {
	// Key is the directive name, spelled as the file spells it.
	Key string
	// Value is everything after the key.
	Value string
}

// Entry is one alias a user can connect to, with the block that defines it.
type Entry struct {
	// Name is the alias, as a user types it after ssh.
	Name string
	// Aliases are every literal pattern of the defining block (`Host a b`).
	Aliases []string
	// Options are the defining block's own directives, in file order.
	Options []Option
	// Resolved are the directives that apply to the alias, from every block
	// that matches it, with ssh's first-value-wins rule.
	Resolved []Option
	// Comments are the comment lines inside the defining block.
	Comments []string
	// Managed is true when tern-ssh wrote the block (it carries the marker).
	Managed bool

	start int // 1-based line of the Host keyword
	end   int // 1-based last line of the block's own directives
	tail  int // 1-based end of the blank and comment lines after it
}

// Opt returns the resolved value of key, or "" when nothing sets it.
func (e Entry) Opt(key string) string {
	return value(e.Resolved, key)
}

// BlockOpt returns the value key has in the defining block alone, "" when the
// block does not set it. Rewriting a block starts from these, so an option
// inherited from another block is not copied into it.
func (e Entry) BlockOpt(key string) string {
	return value(e.Options, key)
}

// value finds key in options, case-insensitively.
func value(options []Option, key string) string {
	for _, o := range options {
		if strings.EqualFold(o.Key, key) {
			return o.Value
		}
	}
	return ""
}

// HostName is the resolved HostName; ssh connects to the alias itself when it
// is unset, so the alias stands in.
func (e Entry) HostName() string {
	if v := e.Opt("hostname"); v != "" {
		return v
	}
	return e.Name
}

// User is the resolved User, "" when the config leaves it to the local user.
func (e Entry) User() string { return e.Opt("user") }

// Port is the resolved Port, "" for ssh's default.
func (e Entry) Port() string { return e.Opt("port") }

// IdentityFile is the resolved IdentityFile, "" when unset.
func (e Entry) IdentityFile() string { return e.Opt("identityfile") }

// ProxyJump is the resolved ProxyJump, "" when unset.
func (e Entry) ProxyJump() string { return e.Opt("proxyjump") }

// Target is the resolved destination, `user@host:port` with the parts the
// config sets (unlike the ssh command line, the alias is what ssh resolves).
func (e Entry) Target() string {
	host := e.HostName()
	if u := e.User(); u != "" {
		host = u + "@" + host
	}
	if p := e.Port(); p != "" {
		host += ":" + p
	}
	return host
}

// File is an ssh config file held in memory.
type File struct {
	// Path is the file's location.
	Path string
	// Lines are the file's lines, without their line endings.
	Lines []string
	// Entries are the connectable aliases found in it, in file order.
	Entries []Entry

	mode os.FileMode
}

// NewEntry builds the entry the manager's form edits, from the plain values
// of a Host block. Empty values are left out of the block.
func NewEntry(name, hostname, user, port, identityFile, proxyJump string) Entry {
	e := Entry{Name: name, Aliases: []string{name}}
	for _, o := range []Option{
		{Key: "HostName", Value: hostname},
		{Key: "User", Value: user},
		{Key: "Port", Value: port},
		{Key: "IdentityFile", Value: identityFile},
		{Key: "ProxyJump", Value: proxyJump},
	} {
		if strings.TrimSpace(o.Value) != "" {
			e.Options = append(e.Options, o)
		}
	}
	return e
}

// DefaultPath is $TERN_SSH_CONFIG when set, else ~/.ssh/config.
func DefaultPath() string {
	if p := os.Getenv("TERN_SSH_CONFIG"); p != "" {
		return expandHome(p)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".ssh", "config")
	}
	return filepath.Join(home, ".ssh", "config")
}

// Load reads path. A missing file is not an error: the File is empty and the
// first write creates it.
func Load(path string) (*File, error) {
	f := &File{Path: expandHome(path), mode: 0o600}
	data, err := os.ReadFile(f.Path)
	switch {
	case os.IsNotExist(err):
		f.refresh()
		return f, nil
	case err != nil:
		return nil, err
	}
	if info, err := os.Stat(f.Path); err == nil {
		f.mode = info.Mode().Perm()
	}
	f.Lines = splitLines(string(data))
	f.refresh()
	return f, nil
}

// Reload reads the file again, dropping unsaved changes.
func (f *File) Reload() error {
	next, err := Load(f.Path)
	if err != nil {
		return err
	}
	*f = *next
	return nil
}

// Find returns the entry named name, nil when the file has none.
func (f *File) Find(name string) *Entry {
	for i := range f.Entries {
		if strings.EqualFold(f.Entries[i].Name, name) {
			return &f.Entries[i]
		}
	}
	return nil
}

// Names returns every alias, in file order.
func (f *File) Names() []string {
	out := make([]string, 0, len(f.Entries))
	for _, e := range f.Entries {
		out = append(out, e.Name)
	}
	return out
}

// Add appends a Host block for e, tagged with the marker, and saves.
func (f *File) Add(e Entry) error {
	if err := validate(e); err != nil {
		return err
	}
	if f.Find(e.Name) != nil {
		return fmt.Errorf("%q is already in %s", e.Name, f.Path)
	}
	if n := len(f.Lines); n > 0 && strings.TrimSpace(f.Lines[n-1]) != "" {
		f.Lines = append(f.Lines, "")
	}
	f.Lines = append(f.Lines, Marker)
	f.Lines = append(f.Lines, format(e)...)
	return f.Save()
}

// Update rewrites the block that defines name with e's fields, at the same
// place in the file, preserving the marker and the comments inside the block.
// e.Name may differ from name: that renames the host.
func (f *File) Update(name string, e Entry) error {
	if err := validate(e); err != nil {
		return err
	}
	old := f.Find(name)
	if old == nil {
		return fmt.Errorf("%q is not in %s", name, f.Path)
	}
	if !strings.EqualFold(name, e.Name) && f.Find(e.Name) != nil {
		return fmt.Errorf("%q is already in %s", e.Name, f.Path)
	}
	e.Comments = old.Comments
	block := format(e)
	block = append(block, extra(*old)...)
	if len(e.Comments) > 0 {
		block = append(block[:1], append(append([]string{}, e.Comments...), block[1:]...)...)
	}
	f.Lines = splice(f.Lines, old.start-1, old.end, block)
	return f.Save()
}

// Remove deletes the block that defines name, its marker and the blank lines
// around it.
func (f *File) Remove(name string) error {
	e := f.Find(name)
	if e == nil {
		return fmt.Errorf("%q is not in %s", name, f.Path)
	}
	from := e.start - 1
	if e.Managed && from > 0 && isMarker(f.Lines[from-1]) {
		from--
	}
	// Take the blank separator above the block with it, so no gap is left.
	if from > 0 && strings.TrimSpace(f.Lines[from-1]) == "" {
		from--
	}
	f.Lines = splice(f.Lines, from, e.tail, nil)
	return f.Save()
}

// Save writes the file back, atomically, keeping its permissions.
func (f *File) Save() error {
	var buf strings.Builder
	for _, line := range f.Lines {
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	dir := filepath.Dir(f.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tern-ssh-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(f.mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(buf.String()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, f.Path); err != nil {
		return err
	}
	f.refresh()
	return nil
}

// format renders e as the lines of a Host block.
func format(e Entry) []string {
	lines := []string{"Host " + e.Name}
	add := func(key, value string) {
		if strings.TrimSpace(value) != "" {
			lines = append(lines, "\t"+key+" "+strings.TrimSpace(value))
		}
	}
	add("HostName", e.BlockOpt("hostname"))
	add("User", e.BlockOpt("user"))
	add("Port", e.BlockOpt("port"))
	add("IdentityFile", e.BlockOpt("identityfile"))
	add("ProxyJump", e.BlockOpt("proxyjump"))
	return lines
}

// fields are the options the manager edits; a block's other options are left
// as they were when it is rewritten.
var fields = map[string]bool{
	"hostname": true, "user": true, "port": true, "identityfile": true, "proxyjump": true,
}

// extra is the block's directives the form does not manage, as lines.
func extra(old Entry) []string {
	var out []string
	for _, o := range old.Options {
		if !fields[strings.ToLower(o.Key)] {
			out = append(out, "\t"+o.Key+" "+o.Value)
		}
	}
	return out
}

// validate rejects an entry ssh could not use.
func validate(e Entry) error {
	switch {
	case e.Name == "":
		return fmt.Errorf("the alias is empty")
	case strings.ContainsAny(e.Name, " \t"):
		return fmt.Errorf("the alias %q contains whitespace", e.Name)
	case strings.ContainsAny(e.Name, "*?!"):
		return fmt.Errorf("the alias %q contains a wildcard", e.Name)
	}
	for _, field := range []struct{ name, value string }{
		{"HostName", e.BlockOpt("hostname")},
		{"User", e.BlockOpt("user")},
		{"IdentityFile", e.BlockOpt("identityfile")},
		{"ProxyJump", e.BlockOpt("proxyjump")},
	} {
		if strings.ContainsAny(field.value, " \t") {
			return fmt.Errorf("%s %q contains whitespace", field.name, field.value)
		}
	}
	if p := strings.TrimSpace(e.BlockOpt("port")); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("Port %q is not a port number", p)
		}
	}
	return nil
}

// refresh parses Lines into Entries.
func (f *File) refresh() {
	f.Entries = nil
	for _, b := range sections(f.Lines) {
		if b.kind != "host" {
			continue
		}
		var literals []string
		for _, p := range b.patterns {
			if !strings.ContainsAny(p, "*?![") {
				literals = append(literals, p)
			}
		}
		for _, name := range literals {
			if f.has(name) {
				continue
			}
			f.Entries = append(f.Entries, Entry{
				Name:     name,
				Aliases:  literals,
				Options:  b.options,
				Comments: b.inside(),
				Managed:  b.marker,
				start:    b.start + 1,
				end:      b.last + 1,
				tail:     b.tail + 1,
			})
		}
	}
	for i := range f.Entries {
		f.Entries[i].Resolved = f.resolve(f.Entries[i].Name)
	}
}

// comment is a comment line of the file, with where it sits.
type comment struct {
	line int
	text string
}

// inside is the comments between the block's keyword and its last directive.
// Comments after that belong to the gap before the next block and are left
// where they are when this block is rewritten.
func (b section) inside() []string {
	var out []string
	for _, c := range b.comments {
		if c.line > b.start && c.line <= b.last {
			out = append(out, c.text)
		}
	}
	return out
}

// has reports whether an entry for name exists already.
func (f *File) has(name string) bool {
	for _, e := range f.Entries {
		if strings.EqualFold(e.Name, name) {
			return true
		}
	}
	return false
}

// resolve collects the options that apply to name, first value wins, the way
// ssh reads the file.
func (f *File) resolve(name string) []Option {
	var out []Option
	seen := map[string]bool{}
	for _, b := range sections(f.Lines) {
		if b.kind != "host" || !b.matches(name) {
			continue
		}
		for _, o := range b.options {
			key := strings.ToLower(o.Key)
			if !seen[key] {
				seen[key] = true
				out = append(out, o)
			}
		}
	}
	return out
}

// section is one block of the file: a Host or Match block, an Include line or
// the leading group of global directives.
type section struct {
	kind     string // "host", "match", "include" or "" for global directives
	patterns []string
	options  []Option
	comments []comment
	start    int // 0-based keyword line
	last     int // 0-based last line holding a directive
	tail     int // 0-based last line before the next section, marker aside
	marker   bool
}

// matches reports whether the block applies to host: a positive pattern has
// to match it and no negated one may, as ssh reads the list.
func (b section) matches(host string) bool {
	matched := false
	for _, p := range b.patterns {
		negated := strings.HasPrefix(p, "!")
		pattern := strings.TrimPrefix(p, "!")
		if !patternMatch(pattern, host) {
			continue
		}
		if negated {
			return false
		}
		matched = true
	}
	return matched
}

// patternMatch reports whether an ssh host pattern matches host: `*` matches
// any run of characters, `?` one, everything else itself, case-insensitively.
func patternMatch(pattern, host string) bool {
	p, h := strings.ToLower(pattern), strings.ToLower(host)
	if p == "" {
		return false
	}
	if p == "*" {
		return true
	}
	// Walk both, backtracking to the last `*`.
	pi, hi, star, mark := 0, 0, -1, 0
	for hi < len(h) {
		switch {
		case pi < len(p) && (p[pi] == h[hi] || p[pi] == '?'):
			pi++
			hi++
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, hi
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			hi = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

// sections splits lines into blocks the way ssh's parser reads them.
func sections(lines []string) []section {
	var out []section
	cur := -1
	for i, raw := range lines {
		line := strings.TrimSuffix(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			if cur >= 0 && strings.HasPrefix(trimmed, "#") {
				out[cur].comments = append(out[cur].comments, comment{line: i, text: line})
			}
			continue
		}
		key, value := split(trimmed)
		if kind, ok := sectionKind(key); ok {
			s := section{kind: kind, start: i, last: i, tail: i}
			if kind != "include" {
				s.patterns = strings.Fields(value)
			}
			if i > 0 && isMarker(lines[i-1]) {
				s.marker = true
			}
			out = append(out, s)
			cur = len(out) - 1
			continue
		}
		if cur < 0 {
			out = append(out, section{start: i, last: i, tail: i})
			cur = 0
		}
		out[cur].options = append(out[cur].options, Option{Key: key, Value: value})
		out[cur].last = i
		out[cur].tail = i
	}
	// A section owns the blank and comment lines up to the next one, but never
	// the marker that introduces it.
	for i := range out {
		if i+1 < len(out) {
			end := out[i+1].start - 1
			if out[i+1].marker {
				end--
			}
			if end >= out[i].last {
				out[i].tail = end
			}
		} else {
			out[i].tail = len(lines) - 1
		}
	}
	return out
}

// sectionKind names the keyword that starts a section.
func sectionKind(key string) (string, bool) {
	switch strings.ToLower(key) {
	case "host":
		return "host", true
	case "match":
		return "match", true
	case "include":
		return "include", true
	}
	return "", false
}

// split cuts a directive line into its key and value, accepting `key value`
// and `key=value`.
func split(line string) (key, value string) {
	i := strings.IndexAny(line, " \t=")
	if i < 0 {
		return line, ""
	}
	return line[:i], strings.TrimSpace(strings.TrimPrefix(line[i:], "="))
}

// isMarker reports whether a line is tern-ssh's marker comment.
func isMarker(line string) bool {
	return strings.EqualFold(strings.TrimSpace(line), Marker)
}

// splice replaces lines[from:to] with repl and returns the result.
func splice(lines []string, from, to int, repl []string) []string {
	if from < 0 {
		from = 0
	}
	if to > len(lines) {
		to = len(lines)
	}
	if from > to {
		from = to
	}
	out := make([]string, 0, len(lines)-(to-from)+len(repl))
	out = append(out, lines[:from]...)
	out = append(out, repl...)
	return append(out, lines[to:]...)
}

// splitLines cuts s into lines, dropping a single trailing newline.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

// expandHome resolves a leading ~ to the user's home directory.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
}
