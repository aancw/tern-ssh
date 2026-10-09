package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stencil-hq/tern-sdk/go/tern"
	"github.com/stencil-hq/tern-sdk/go/tern/ui"

	"github.com/aancw/tern-ssh/internal/sshconf"
)

// view is the whole surface.
func (a *App) view() tern.View {
	switch a.mode {
	case ModeForm:
		return tern.View{Main: a.formView()}
	case ModeUser:
		return tern.View{Main: a.userView()}
	}
	return tern.View{Main: a.listView()}
}

// listView is the header, the filter, the host list and the selected host's
// details.
func (a *App) listView() tern.Element {
	rows := a.rows()

	items := make([]tern.Element, 0, len(rows))
	for _, e := range rows {
		items = append(items, ui.Item{
			Common: ui.Common{Title: e.Target()},
			Label:  ui.T(e.Name),
			Detail: ui.T(detail(e)),
			Value:  ui.T(source(e)),
			Icon:   ui.IconServer,
		})
	}
	selected := fmt.Sprintf("%s.%d", listID, a.cursor)
	if len(rows) == 0 {
		selected = ""
	}

	head := ui.Row{
		Common: ui.Common{Key: "head"},
		Gap:    ui.GapSM,
		Align:  ui.AlignCenter,
		Children: ui.Nodes(
			ui.Icon{Name: ui.IconServer},
			ui.Text{Text: "SSH hosts", Common: ui.Common{Key: "title"}},
			ui.Text{Text: count(len(rows), len(a.hosts)), Common: ui.Common{Key: "count", Tone: ui.ToneMuted}},
		),
	}

	list := ui.List{
		CommonWithoutMax: ui.CommonWithoutMax{Key: listKey},
		Selected:         selected,
		Filter:           a.filter,
		Empty:            ui.T("No hosts here. Press a to add one."),
		Max:              ui.MaxFrac(0.6),
		Children:         items,
	}

	body := []tern.Element{
		head,
		a.statusNode(),
		ui.Text{
			Text:   collapse(a.path),
			Common: ui.Common{Key: "path", Tone: ui.ToneMuted},
		},
		a.filterNode(),
		list,
	}
	if a.info {
		body = append(body, ui.Rule{Common: ui.Common{Key: "rule"}}, a.detailNode())
	}
	if a.mode == ModeConfirm {
		body = append(body, ui.Text{
			Common: ui.Common{Key: "confirm", Tone: ui.ToneWarning},
			Spans:  []ui.Span{{T: fmt.Sprintf("Remove %s from %s?", a.confirm, collapse(a.path))}, {T: "  y", S: "strong"}, {T: " remove   "}, {T: "n", S: "strong"}, {T: " keep"}},
		})
	}
	body = append(body, ui.Text{
		Text:   help,
		Common: ui.Common{Key: "help", Tone: ui.ToneMuted},
	})
	return ui.Col{
		Common:   ui.Common{Key: "root"},
		Gap:      ui.GapSM,
		Align:    ui.AlignStart,
		Children: ui.Nodes(body...),
	}
}

// statusNode is the last note on its own line, empty when there is none.
func (a *App) statusNode() tern.Element {
	if a.status == "" {
		return ui.Text{Common: ui.Common{Key: "status", Hidden: true}}
	}
	icon, tone := ui.IconCheck, ui.ToneSuccess
	if a.failed {
		icon, tone = ui.IconWarn, ui.ToneError
	}
	return ui.Row{
		Common: ui.Common{Key: "status"},
		Gap:    ui.GapSM,
		Align:  ui.AlignCenter,
		Children: ui.Nodes(
			ui.Icon{Name: icon, Common: ui.Common{Tone: tone}},
			ui.Text{Text: a.status, Common: ui.Common{Grow: 1, Tone: tone}},
		),
	}
}

// filterNode is the filter line: what is being typed, what is on, or the hint.
func (a *App) filterNode() tern.Element {
	switch {
	case a.mode == ModeFilter:
		return ui.Text{
			Common: ui.Common{Key: "filter"},
			Spans:  []ui.Span{{T: "/ "}, {T: a.filter}, {T: "▏", S: "accent"}},
		}
	case a.filter != "":
		return ui.Text{
			Common: ui.Common{Key: "filter"},
			Spans:  []ui.Span{{T: "/ "}, {T: a.filter, S: "strong"}, {T: "  esc clears", S: "muted"}},
		}
	}
	return ui.Text{Text: "press / to filter", Common: ui.Common{Key: "filter", Tone: ui.ToneMuted}}
}

// detailNode shows the selected host, the file's options for it.
func (a *App) detailNode() tern.Element {
	e, ok := a.selected()
	if !ok {
		return ui.Text{
			Text:   "No host selected.",
			Common: ui.Common{Key: "detail", Tone: ui.ToneMuted},
		}
	}
	items := []ui.Pair{
		{K: ui.T("destination"), V: ui.T(e.Target())},
		{K: ui.T("user"), V: value(e.User())},
		{K: ui.T("port"), V: value(e.Port())},
		{K: ui.T("identity"), V: value(e.IdentityFile())},
		{K: ui.T("jump"), V: value(e.ProxyJump())},
		{K: ui.T("source"), V: ui.T(source(e))},
	}
	if len(e.Aliases) > 1 {
		others := []string{}
		for _, alias := range e.Aliases {
			if !strings.EqualFold(alias, e.Name) {
				others = append(others, alias)
			}
		}
		items = append(items, ui.Pair{K: ui.T("also"), V: ui.T(strings.Join(others, " "))})
	}
	return ui.Card{
		Common:   ui.Common{Key: "detail"},
		Head:     ui.Spans(ui.Span{T: e.Name, S: "strong"}, ui.Span{T: "  " + sshCommand(e.Name), S: "muted"}),
		Inset:    true,
		Children: ui.Nodes(ui.KV{Items: items}),
	}
}

// userView asks which login name to use for a host whose block sets no User.
func (a *App) userView() tern.Element {
	host := a.selectedName()
	spans := []ui.Span{}
	if a.user == "" {
		spans = append(spans, ui.Span{T: localUser(), S: "muted"})
	} else {
		spans = append(spans, ui.Span{T: a.user})
	}
	spans = append(spans, ui.Span{T: "▏", S: "accent"})

	destination := ""
	if e, ok := a.selected(); ok {
		destination = e.Target()
	}
	return ui.Col{Common: ui.Common{Key: "root"}, Gap: ui.GapSM, Align: ui.AlignStart, Children: ui.Nodes(
		ui.Card{Common: ui.Common{Key: "prompt"}, Head: ui.T("Log in to " + host), Children: ui.Nodes(
			ui.Text{Text: destination, Common: ui.Common{Key: "target", Tone: ui.ToneMuted}},
			ui.Row{
				Common: ui.Common{Key: "user"},
				Gap:    ui.GapSM,
				Children: ui.Nodes(
					ui.Text{Text: "user", Common: ui.Common{Tone: ui.ToneAccent, Min: &ui.Bounds{W: ui.Ch(labelWidth)}}},
					ui.Text{Common: ui.Common{Key: "value", Grow: 1}, Spans: spans},
				),
			},
		)},
		ui.Text{
			Text:   "This host sets no User. Enter connects as " + localUser() + " unless you type a name.",
			Common: ui.Common{Key: "hint", Tone: ui.ToneMuted},
		},
		ui.Text{
			Text:   "enter connect   esc cancel",
			Common: ui.Common{Key: "help", Tone: ui.ToneMuted},
		},
	)}
}

// localUser is the login name ssh would use with no User set.
func localUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "the local user"
}

// formView is the add or edit form.
func (a *App) formView() tern.Element {
	title := "New host"
	if a.editing {
		title = "Edit " + a.fields[FieldAlias]
	}
	rows := make([]tern.Element, 0, numFields)
	for i := range numFields {
		f := Field(i)
		spans := []ui.Span{}
		switch {
		case a.fields[f] != "":
			spans = append(spans, ui.Span{T: a.fields[f]})
		default:
			spans = append(spans, ui.Span{T: Hints[f], S: "muted"})
		}
		if f == a.field {
			spans = append(spans, ui.Span{T: "▏", S: "accent"})
		}
		labelTone, valueTone := ui.ToneMuted, ui.Tone("")
		if f == a.field {
			labelTone = ui.ToneAccent
		}
		rows = append(rows, ui.Row{
			Common: ui.Common{Key: Labels[f]},
			Gap:    ui.GapSM,
			Children: ui.Nodes(
				ui.Text{Text: Labels[f], Common: ui.Common{Tone: labelTone, Min: &ui.Bounds{W: ui.Ch(labelWidth)}}},
				ui.Text{Common: ui.Common{Grow: 1, Tone: valueTone}, Spans: spans},
			),
		})
	}
	return ui.Col{Common: ui.Common{Key: "root"}, Gap: ui.GapSM, Align: ui.AlignStart, Children: ui.Nodes(
		ui.Card{Common: ui.Common{Key: "form"}, Head: ui.T(title), Children: ui.Nodes(rows...)},
		a.statusNode(),
		ui.Text{Text: Hints[a.field], Common: ui.Common{Key: "hint", Tone: ui.ToneMuted}},
		ui.Text{
			Text:   "tab next   enter save   esc cancel",
			Common: ui.Common{Key: "help", Tone: ui.ToneMuted},
		},
	)}
}

// detail is an item row's secondary text.
func detail(e sshconf.Entry) string {
	user := e.User()
	if user == "" {
		user = "?"
	}
	port := e.Port()
	if port == "" {
		port = "22"
	}
	return fmt.Sprintf("%s@%s:%s", user, e.HostName(), port)
}

// source names where a host comes from.
func source(e sshconf.Entry) string {
	if e.Managed {
		return "tern-ssh"
	}
	return "config"
}

// value is an optional option value, shown as a dash when unset.
func value(v string) ui.Rich {
	if v == "" {
		return ui.Spans(ui.Span{T: "-", S: "muted"})
	}
	return ui.T(v)
}

// count is the header's host count.
func count(shown, total int) string {
	switch {
	case total == 0:
		return "no hosts yet"
	case shown == total:
		return fmt.Sprintf("%d host%s", total, plural(total))
	}
	return fmt.Sprintf("%d of %d hosts", shown, total)
}

// plural is "s" for everything but one.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// sshCommand is the command line a host stands for.
func sshCommand(alias string) string { return "ssh " + alias }

// collapse writes a path under the home directory with a leading ~.
func collapse(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(home, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return "~/" + rel
}
