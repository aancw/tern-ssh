// Package app is tern-ssh's manager: the host list, the add and edit form,
// and the hand-off to a new ssh session. Inside Tern it draws with the Tern
// Surface Protocol; anywhere else it prints the list as plain text.
package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/stencil-hq/tern-sdk/go/tern"

	"github.com/aancw/tern-ssh/internal/sshconf"
)

// Version is the program version, reported to Tern in hello.
const Version = "0.1.0"

// node ids the view derives from the list's key (main.<key>.<index>).
const (
	listKey = "hosts"
	listID  = "main." + listKey
)

const help = "enter connect   a add   e edit   d delete   i info   / filter   r reload   q quit"

// handOver asks Run to give this pane to an ssh session, then come back.
type handOver struct{ host, config, user string }

// park: where a connect request goes, for the beside/new-tab keys parked in
// the list's key switch. Bring it back with them.
//
//	type Connect int
//
//	const (
//		Here Connect = iota
//		Beside
//		Tab
//	)

// Error names what is being handed over.
func (h *handOver) Error() string { return "hand this pane to ssh " + h.host }

// Field is one input of the add and edit form.
type Field int

// The form's fields, in tab order.
const (
	FieldAlias Field = iota
	FieldHostName
	FieldUser
	FieldPort
	FieldIdentity
	FieldProxyJump
	numFields
)

// Labels names each field.
var Labels = [numFields]string{"Alias", "HostName", "User", "Port", "IdentityFile", "ProxyJump"}

// labelWidth is the field-name column of the form and the prompt: wide enough
// for the longest label, so the values line up under each other at the left.
const labelWidth = 13

// Hints describes what each field is for.
var Hints = [numFields]string{
	"the name you ssh to",
	"the address ssh connects to (default: the alias)",
	"login user",
	"port (default 22)",
	"private key path",
	"host to jump through",
}

// Mode is what the keys and the view do now.
type Mode int

// The manager's modes.
const (
	// ModeList browses the hosts.
	ModeList Mode = iota
	// ModeFilter types into the filter above the list.
	ModeFilter
	// ModeForm edits one host.
	ModeForm
	// ModeConfirm asks before removing a host.
	ModeConfirm
	// ModeUser asks which login name to use.
	ModeUser
)

// App is the manager's state.
type App struct {
	path  string
	file  *sshconf.File
	hosts []sshconf.Entry

	mode   Mode
	filter string
	cursor int

	editing     bool
	editingName string // the alias the block has now, so an edit can rename it
	fields      [numFields]string
	field       Field
	confirm     string

	user string // the login name typed into the prompt

	info bool // show the selected host's details

	status string
	failed bool

	cols int
	sf   *tern.Surface
}

// New loads the config at path and returns a manager for it.
func New(path string) (*App, error) {
	f, err := sshconf.Load(path)
	if err != nil {
		return nil, err
	}
	a := &App{path: f.Path, file: f}
	a.reload()
	return a, nil
}

// Path is the config file the manager edits.
func (a *App) Path() string { return a.path }

// Hosts returns every host in the file, in file order.
func (a *App) Hosts() []sshconf.Entry { return a.hosts }

// Run draws the manager until the user quits. Outside Tern it prints the
// host list instead.
func (a *App) Run(ctx context.Context) error {
	s, err := tern.Connect(ctx, tern.Options{App: "tern-ssh", Version: Version})
	if errors.Is(err, tern.ErrUnsupported) {
		fmt.Print(a.Plain())
		return nil
	}
	if err != nil {
		return err
	}
	defer s.Close()

	sf, err := s.Open(tern.SurfaceOptions{Mode: tern.Screen, Title: "SSH", Role: "tern-ssh", Discard: true})
	if err != nil {
		return err
	}
	a.sf = sf
	a.cols = s.Caps().Cols
	if err := a.draw(); err != nil {
		return err
	}
	err = s.Run(ctx, a.handle)
	if hand, ok := errors.AsType[*handOver](err); ok {
		// Close restores the terminal and joins the reader before ssh takes
		// the pty over.
		if err := s.Close(); err != nil {
			return err
		}
		return execSession(hand.host, hand.config, hand.user)
	}
	if err != nil && !errors.Is(err, tern.ErrStop) {
		return err
	}
	return nil
}

// handle applies one input.
func (a *App) handle(in tern.Input) error {
	switch in := in.(type) {
	case tern.Key:
		return a.key(in)
	case *tern.Resize:
		a.cols = in.Cols
		return a.draw()
	case *tern.Select:
		a.pick(in.Item)
		return a.draw()
	case *tern.Activate:
		a.pick(in.Item)
		return a.connect()
	case *tern.ErrorEvent:
		return fmt.Errorf("tern: %s", in.Msg)
	}
	return nil
}

// draw renders the current state.
func (a *App) draw() error {
	if !a.sf.Closed() {
		return a.sf.Render(a.view())
	}
	return nil
}

// key applies one key press, per mode.
func (a *App) key(k tern.Key) error {
	switch a.mode {
	case ModeForm:
		return a.formKey(k)
	case ModeConfirm:
		return a.confirmKey(k)
	case ModeUser:
		return a.userKey(k)
	case ModeFilter:
		return a.filterKey(k)
	}
	return a.listKey(k)
}

// listKey browses and starts the other modes.
func (a *App) listKey(k tern.Key) error {
	switch {
	case named(k, "q", "ctrl+c", "ctrl+d"):
		return tern.ErrStop
	case named(k, "escape"):
		if a.filter != "" {
			a.filter, a.cursor = "", 0
			return a.draw()
		}
		return tern.ErrStop
	case named(k, "j", "down", "ctrl+n"):
		return a.move(1)
	case named(k, "k", "up", "ctrl+p"):
		return a.move(-1)
	case named(k, "g", "home"):
		a.cursor = 0
	case named(k, "G", "shift+g", "end"):
		a.cursor = a.count() - 1
	case named(k, "/"):
		a.mode = ModeFilter
	case named(k, "enter", "o", "O"):
		return a.connect()
	// park: connecting beside this pane, or in a new tab. The mechanism is
	// commented in connect.go with why it is off and what reviving it needs.
	//
	//	case named(k, "s", "S"):
	//		return a.connect(Beside)
	//	case named(k, "t", "T", "shift+t"):
	//		return a.connect(Tab)
	case named(k, "a", "A"):
		a.startForm(nil)
	case named(k, "e", "E"):
		if e, ok := a.selected(); ok {
			a.startForm(&e)
		}
	case named(k, "d", "D"):
		if e, ok := a.selected(); ok {
			a.confirm, a.mode = e.Name, ModeConfirm
		}
	case named(k, "i", "I"):
		a.info = !a.info
	case named(k, "r", "R"):
		return a.reloadKey()
	}
	return a.draw()
}

// filterKey types the filter.
func (a *App) filterKey(k tern.Key) error {
	switch {
	case named(k, "escape"):
		a.filter, a.cursor, a.mode = "", 0, ModeList
	case named(k, "enter"):
		a.mode = ModeList
		return a.connect()
	case named(k, "j", "down"):
		return a.move(1)
	case named(k, "k", "up"):
		return a.move(-1)
	case named(k, "backspace"):
		a.filter = chop(a.filter)
		a.cursor = 0
	default:
		a.filter += k.Text
		a.cursor = 0
	}
	return a.draw()
}

// formKey edits the form.
func (a *App) formKey(k tern.Key) error {
	switch {
	case named(k, "escape"):
		a.mode = ModeList
		a.status = ""
	case named(k, "tab", "down"):
		a.field = (a.field + 1) % numFields
	case named(k, "shift+tab", "up"):
		a.field = (a.field + numFields - 1) % numFields
	case named(k, "enter"):
		return a.save()
	case named(k, "backspace"):
		a.fields[a.field] = chop(a.fields[a.field])
	case named(k, "ctrl+u"):
		a.fields[a.field] = ""
	default:
		if !k.Ctrl && !k.Meta && !k.Alt {
			a.fields[a.field] += k.Text
		}
	}
	return a.draw()
}

// confirmKey answers the removal prompt.
func (a *App) confirmKey(k tern.Key) error {
	switch {
	case named(k, "y", "Y", "enter"):
		name := a.confirm
		a.confirm, a.mode = "", ModeList
		if err := a.file.Remove(name); err != nil {
			a.fail(err)
		} else {
			a.reload()
			a.ok("removed %s", name)
		}
	case named(k, "n", "N", "escape", "q"):
		a.confirm, a.mode = "", ModeList
	}
	return a.draw()
}

// save writes the form's host to the file.
func (a *App) save() error {
	e := sshconf.NewEntry(
		strings.TrimSpace(a.fields[FieldAlias]),
		strings.TrimSpace(a.fields[FieldHostName]),
		strings.TrimSpace(a.fields[FieldUser]),
		strings.TrimSpace(a.fields[FieldPort]),
		strings.TrimSpace(a.fields[FieldIdentity]),
		strings.TrimSpace(a.fields[FieldProxyJump]),
	)
	var err error
	if a.editing {
		err = a.file.Update(a.editingName, e)
	} else {
		err = a.file.Add(e)
	}
	if err != nil {
		a.fail(err)
		return a.draw()
	}
	name, wasEditing := e.Name, a.editing
	a.mode = ModeList
	a.reload()
	if i := a.indexOf(name); i >= 0 {
		a.cursor = i
	}
	if wasEditing {
		a.ok("updated %s", name)
	} else {
		a.ok("added %s", name)
	}
	return a.draw()
}

// startForm opens the add or edit form, prefilled from old.
func (a *App) startForm(old *sshconf.Entry) {
	a.editing, a.fields, a.field = old != nil, [numFields]string{}, FieldAlias
	a.mode, a.status, a.editingName = ModeForm, "", ""
	if old == nil {
		return
	}
	a.editingName = old.Name
	a.fields = [numFields]string{
		old.Name,
		old.BlockOpt("hostname"),
		old.BlockOpt("user"),
		old.BlockOpt("port"),
		old.BlockOpt("identityfile"),
		old.BlockOpt("proxyjump"),
	}
}

// connect gives this pane to a session for the selected host, asking for the
// login name first when the config sets no User for it.
func (a *App) connect() error {
	e, ok := a.selected()
	if !ok {
		return a.draw()
	}
	if e.User() == "" {
		a.user, a.mode, a.status = "", ModeUser, ""
		return a.draw()
	}
	return a.session(e.Name, e.User())
}

// userKey types the login name the prompt asks for, then connects.
func (a *App) userKey(k tern.Key) error {
	switch {
	case named(k, "escape"):
		a.mode = ModeList
	case named(k, "enter"):
		name, user := a.selectedName(), strings.TrimSpace(a.user)
		a.mode = ModeList
		return a.session(name, user)
	case named(k, "backspace"):
		a.user = chop(a.user)
	case named(k, "ctrl+u"):
		a.user = ""
	default:
		if !k.Ctrl && !k.Meta && !k.Alt {
			a.user += k.Text
		}
	}
	return a.draw()
}

// session hands this pane to ssh for host, logging in as user when one is set.
func (a *App) session(host, user string) error {
	if host == "" {
		return a.draw()
	}
	return &handOver{host: host, config: a.path, user: user}
}

// selectedName is the selected host's alias, "" when nothing is selected.
func (a *App) selectedName() string {
	if e, ok := a.selected(); ok {
		return e.Name
	}
	return ""
}

// reloadKey reads the file again.
func (a *App) reloadKey() error {
	if err := a.file.Reload(); err != nil {
		a.fail(err)
		return a.draw()
	}
	a.reload()
	a.ok("reloaded %s", a.path)
	return a.draw()
}

// reload refreshes the host list from the file.
func (a *App) reload() {
	a.hosts = a.file.Entries
	a.clamp()
}

// ok sets a success note.
func (a *App) ok(format string, args ...any) {
	a.status, a.failed = fmt.Sprintf(format, args...), false
}

// fail sets an error note.
func (a *App) fail(err error) {
	a.status, a.failed = err.Error(), true
}

// indexOf returns the position of name in the filtered list, -1 when hidden.
func (a *App) indexOf(name string) int {
	for i, e := range a.rows() {
		if strings.EqualFold(e.Name, name) {
			return i
		}
	}
	return -1
}

// rows is the filtered host list the view and the keys work on.
func (a *App) rows() []sshconf.Entry {
	if a.filter == "" {
		return a.hosts
	}
	q := strings.ToLower(a.filter)
	out := make([]sshconf.Entry, 0, len(a.hosts))
	for _, e := range a.hosts {
		if matches(e, q) {
			out = append(out, e)
		}
	}
	return out
}

// matches reports whether the filter q (lowercase) names e.
func matches(e sshconf.Entry, q string) bool {
	haystack := strings.ToLower(strings.Join([]string{e.Name, e.HostName(), e.User(), e.Port(), e.IdentityFile(), e.ProxyJump()}, " "))
	for _, alias := range e.Aliases {
		haystack += " " + strings.ToLower(alias)
	}
	return strings.Contains(haystack, q)
}

// count is how many hosts the filter leaves.
func (a *App) count() int { return len(a.rows()) }

// selected is the host under the cursor.
func (a *App) selected() (sshconf.Entry, bool) {
	rows := a.rows()
	if a.cursor < 0 || a.cursor >= len(rows) {
		return sshconf.Entry{}, false
	}
	return rows[a.cursor], true
}

// move walks the cursor by delta and clamps it.
func (a *App) move(delta int) error {
	a.cursor += delta
	a.clamp()
	return a.draw()
}

// clamp keeps the cursor inside the list.
func (a *App) clamp() {
	if n := a.count(); a.cursor >= n {
		a.cursor = n - 1
	}
	if a.cursor < 0 {
		a.cursor = 0
	}
}

// pick moves the cursor to the host a click landed on.
func (a *App) pick(item string) {
	key, ok := strings.CutPrefix(item, listID+".")
	if !ok {
		return
	}
	if i, err := strconv.Atoi(key); err == nil && i >= 0 && i < a.count() {
		a.cursor = i
	}
}

// named reports whether k is any of the chords or key names.
func named(k tern.Key, chords ...string) bool {
	for _, c := range chords {
		if k.Name == c || k.Is(c) {
			return true
		}
	}
	return false
}

// chop drops the last character of s.
func chop(s string) string {
	runes := []rune(s)
	if len(runes) == 0 {
		return s
	}
	return string(runes[:len(runes)-1])
}
