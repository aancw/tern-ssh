// Command tern-ssh is an ssh host manager for Tern: it lists the hosts in an
// ssh config, connects to them in new blocks, and adds, edits and removes
// them in place. Inside Tern it draws a native pane with the Tern Surface
// Protocol; anywhere else it prints the same list as plain text.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/aancw/tern-ssh/internal/app"
	"github.com/aancw/tern-ssh/internal/sshconf"
)

const usage = `tern-ssh ` + app.Version + ` — the hosts of an ssh config

usage:
  tern-ssh                      the host manager (plain text outside Tern)
  tern-ssh list                 the hosts as plain text
  tern-ssh add ALIAS [FIELDS]   add a host
  tern-ssh edit ALIAS [FIELDS]  change a host's fields
  tern-ssh remove ALIAS         remove a host and its block
  tern-ssh connect ALIAS [-t]   ssh to ALIAS in a new Tern block

fields:
  -H, --hostname HOST    the address ssh connects to
  -u, --user USER        login user
  -p, --port PORT        port
  -i, --identity FILE    private key path
  -J, --jump HOST        host to jump through
  -l, --login USER       log in as USER when connecting

options:
  -c, --config PATH      the config file (default $TERN_SSH_CONFIG or ~/.ssh/config)
  -v, --version          print the version
  -h, --help             print this help

Inside Tern: enter connects, t opens a tab, a adds, e edits, d removes,
/ filters, r reloads, q quits.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, errUsage) {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "tern-ssh: "+err.Error())
		os.Exit(1)
	}
}

// errUsage asks for the help text.
var errUsage = errors.New("usage")

// options are the command line's fields.
type options struct {
	command  string
	config   string
	hostname string
	user     string
	port     string
	identity string
	jump     string
	login    string
	args     []string
}

func run(argv []string) error {
	opts, err := parse(argv)
	if err != nil {
		return err
	}
	switch opts.command {
	case "help":
		fmt.Print(usage)
		return nil
	case "version":
		fmt.Println("tern-ssh " + app.Version)
		return nil
	case "list":
		a, err := app.New(opts.config)
		if err != nil {
			return err
		}
		fmt.Print(a.Plain())
		return nil
	case "add":
		return edit(opts, false)
	case "edit":
		return edit(opts, true)
	case "remove":
		return remove(opts)
	case "connect":
		return connect(opts)
	}
	a, err := app.New(opts.config)
	if err != nil {
		return err
	}
	return a.Run(context.Background())
}

// edit adds a host, or rewrites one.
func edit(opts *options, update bool) error {
	alias, err := aliasOf(opts)
	if err != nil {
		return err
	}
	f, err := sshconf.Load(opts.config)
	if err != nil {
		return err
	}
	e := sshconf.NewEntry(alias, opts.hostname, opts.user, opts.port, opts.identity, opts.jump)
	if update {
		old := f.Find(alias)
		if old == nil {
			return fmt.Errorf("%s is not in %s", alias, opts.config)
		}
		for _, o := range e.Options {
			old.Options = setOption(old.Options, o)
		}
		e = *old
		e.Comments = nil
		err = f.Update(alias, e)
	} else {
		err = f.Add(e)
	}
	if err != nil {
		return err
	}
	fmt.Printf("%s %s in %s\n", verb(update), alias, opts.config)
	return nil
}

// remove deletes a host's block.
func remove(opts *options) error {
	alias, err := aliasOf(opts)
	if err != nil {
		return err
	}
	f, err := sshconf.Load(opts.config)
	if err != nil {
		return err
	}
	if err := f.Remove(alias); err != nil {
		return err
	}
	fmt.Printf("removed %s from %s\n", alias, opts.config)
	return nil
}

// connect opens an ssh session for the alias.
func connect(opts *options) error {
	alias, err := aliasOf(opts)
	if err != nil {
		return err
	}
	// ssh takes this terminal over, the way typing it would.
	argv := []string{}
	if opts.config != sshconf.DefaultPath() {
		argv = append(argv, "-F", opts.config)
	}
	if opts.login != "" {
		argv = append(argv, "-l", opts.login)
	}
	cmd := exec.Command("ssh", append(argv, alias)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// aliasOf is the one positional argument a command takes.
func aliasOf(opts *options) (string, error) {
	switch len(opts.args) {
	case 1:
		return opts.args[0], nil
	case 0:
		return "", fmt.Errorf("%s needs the alias of a host\n\n%s", opts.command, usage)
	}
	return "", fmt.Errorf("%s takes one alias, got %d", opts.command, len(opts.args))
}

// verb names what edit did.
func verb(update bool) string {
	if update {
		return "updated"
	}
	return "added"
}

// setOption replaces key in options, or appends it.
func setOption(options []sshconf.Option, o sshconf.Option) []sshconf.Option {
	if o.Value == "" {
		return options
	}
	for i := range options {
		if strings.EqualFold(options[i].Key, o.Key) {
			options[i].Value = o.Value
			return options
		}
	}
	return append(options, o)
}

// parse reads the command line.
func parse(argv []string) (*options, error) {
	opts := &options{config: sshconf.DefaultPath()}
	if len(argv) == 0 {
		return opts, nil
	}
	if !strings.HasPrefix(argv[0], "-") {
		opts.command, argv = argv[0], argv[1:]
	}
	value := func(field *string, key, inline string, hasInline bool, i *int) error {
		if hasInline {
			*field = inline
			return nil
		}
		*i++
		if *i >= len(argv) {
			return fmt.Errorf("%s needs a value", key)
		}
		*field = argv[*i]
		return nil
	}
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		key, inline, hasInline := strings.Cut(arg, "=")
		take := func(field *string) error { return value(field, key, inline, hasInline, &i) }
		switch key {
		case "-h", "--help":
			opts.command = "help"
		case "-v", "--version":
			opts.command = "version"
		case "-c", "--config":
			if err := take(&opts.config); err != nil {
				return nil, err
			}
		case "-H", "--hostname":
			if err := take(&opts.hostname); err != nil {
				return nil, err
			}
		case "-u", "--user":
			if err := take(&opts.user); err != nil {
				return nil, err
			}
		case "-p", "--port":
			if err := take(&opts.port); err != nil {
				return nil, err
			}
		case "-i", "--identity":
			if err := take(&opts.identity); err != nil {
				return nil, err
			}
		case "-J", "--jump":
			if err := take(&opts.jump); err != nil {
				return nil, err
			}
		case "-l", "--login":
			if err := take(&opts.login); err != nil {
				return nil, err
			}
		default:
			if strings.HasPrefix(arg, "-") {
				return nil, fmt.Errorf("unknown option %s\n\n%s", arg, usage)
			}
			opts.args = append(opts.args, arg)
		}
	}
	return opts, nil
}
