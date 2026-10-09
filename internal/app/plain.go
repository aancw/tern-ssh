package app

import (
	"fmt"
	"strings"
	"text/tabwriter"
)

// Plain renders the host list as the aligned text tern-ssh prints when Tern
// is not drawing the manager (outside Tern, or with TERN_TSP=0).
func (a *App) Plain() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %d host%s\n\n", collapse(a.path), len(a.hosts), plural(len(a.hosts)))
	if len(a.hosts) == 0 {
		b.WriteString("The file has no Host blocks yet. Run `tern-ssh` inside Tern to add one,\n")
		b.WriteString("or `tern-ssh add ALIAS --hostname HOST`.\n")
		return b.String()
	}
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ALIAS\tDESTINATION\tIDENTITY\tSOURCE")
	for _, e := range a.hosts {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.Name, e.Target(), dash(e.IdentityFile()), source(e))
	}
	w.Flush()
	b.WriteString("\nInside Tern this is the interactive host manager: enter connects,\n")
	b.WriteString("a adds, e edits, d removes, / filters.\n")
	return b.String()
}

// dash is s, or a dash when it is empty.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
