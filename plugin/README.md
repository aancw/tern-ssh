# SSH Hosts, a Tern plugin

Adds two palette rows to Tern, one per placement, and claims
`ssh://user@host:port` links.

| Row | Action id | What it does | Default chord |
| --- | --- | --- | --- |
| SSH hosts | `plugin.ternssh.open` | opens the manager in a new tab | none |
| SSH hosts split | `plugin.ternssh.split` | opens it in a split of the focused pane | `cmd+shift+h` |

A command cannot see which chord ran it, so the placement is a row instead of a
modifier. One chord runs one action, so only `split` carries a default; bind
the other in `settings.json` if you want it on a chord too. A clicked `ssh://`
link opens a session for that host in a split of the pane it was clicked in.

The manager itself is the `tern-ssh` command, a Surface Protocol program Tern
draws natively. That is why this package has no host half and no block: Tern
needs no plugin to draw the manager, and the same program runs in any other
terminal as plain text.

## Rows and chords

Both rows live in the **SSH** palette group. A chord Tern's own keymap already
takes is dropped with a `plugin bind left out` warning in the log, so bind what
you use in `settings.json`:

```json
{ "keybinds": {
	"cmd+shift+h": "plugin.ternssh.split",
	"cmd+alt+shift+t": "plugin.ternssh.open"
} }
```

## Requirements

`tern-ssh` on PATH in the pane's shell, or `$TERN_SSH_BIN` naming it:

```sh
go install github.com/aancw/tern-ssh@latest
```

## Install

```sh
tern plugin install /path/to/tern-ssh/plugin     # or, from a checkout:
tern plugin link /path/to/tern-ssh/plugin
tern plugin reload
```

`tern plugin list` should show `ternssh … ready`.

## Files

| File | Holds |
| --- | --- |
| `plugin.toml` | the manifest: window half only |
| `window.luau` | the palette rows and the `ssh://` link route |
