package app

import (
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/aancw/tern-ssh/internal/sshconf"
)

// execSession gives this pane to an ssh session and, when it ends, runs
// tern-ssh again in the same pane, so the manager comes back. It only returns
// on failure: on success the process image is ssh's shell.
func execSession(alias, config, user string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	line := ""
	for _, arg := range remote(alias, config, user) {
		line += quote(arg) + " "
	}
	line += "; exec " + quote(self)
	for _, arg := range os.Args[1:] {
		line += " " + quote(arg)
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		return err
	}
	return syscall.Exec(sh, []string{"sh", "-c", line}, os.Environ())
}

// remote is the argv that starts an ssh session for alias, through Tern's own
// ssh when the tern CLI is installed. A config file other than ssh's own
// default is passed with -F, so the aliases the manager shows are the ones
// ssh resolves, and user, when set, is the login name to use whatever the
// file says.
func remote(alias, config, user string) []string {
	argv := []string{}
	if config != "" && config != sshconf.DefaultPath() {
		argv = append(argv, "-F", config)
	}
	if user != "" {
		argv = append(argv, "-l", user)
	}
	argv = append(argv, alias)
	if ternBin, err := exec.LookPath("tern"); err == nil {
		return append([]string{ternBin, "ssh"}, argv...)
	}
	return append([]string{"ssh"}, argv...)
}

// park: opening a session in a block beside this pane, or in a new tab.
//
// It is parked, not dropped, because a pane cannot say which window it belongs
// to: `tern split` takes the window from TERN_WINDOW_KEY, and Tern does not
// always set it, so the CLI falls back to the first window and the block can
// land next to another window's pane. Shown to the user as an error instead.
//
// To revive: bring back the keys in app.go (marked park too), give connect a
// "where" again, and uncomment this. A fix would either find the window
// reliably (a key the pane can read, or a plugin-mediated request the window
// half acts on) or accept that the first window is the right one.
//
// // spawn runs the session in a block Tern opens: beside this pane, or in a
// // new tab. It asks the tern CLI, which resolves the window from its
// // environment.
// func spawn(alias, config, user string, tab bool) error {
// 	ternBin, err := exec.LookPath("tern")
// 	if err != nil {
// 		return fmt.Errorf("the tern CLI is not on PATH: run `ssh %s` yourself", alias)
// 	}
// 	args := []string{"split", "@focused", "down", "--keep-open", "--"}
// 	if tab {
// 		args = []string{"new", "tab", "--keep-open", "--"}
// 	}
// 	args = append(args, remote(alias, config, user)...)
// 	cmd := exec.Command(ternBin, args...)
// 	var out bytes.Buffer
// 	cmd.Stdout, cmd.Stderr = &out, &out
// 	if err := cmd.Run(); err != nil {
// 		if msg := strings.TrimSpace(out.String()); msg != "" {
// 			return errors.New(msg)
// 		}
// 		return err
// 	}
// 	return nil
// }
//
// // errNoWindow is what spawn returns when the pane carries no window key, so
// // the CLI would aim at whichever window it picks rather than this one.
// var errNoWindow = errors.New("this pane has no window key, so a block cannot be opened beside it without guessing the window — press enter to connect in this pane instead")

// quote single-quotes s for a POSIX shell.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
