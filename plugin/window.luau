--!strict
-- tern-ssh, window half.
--
-- The manager itself is the `tern-ssh` command: a Surface Protocol program
-- Tern draws natively, so it works in every Tern client and needs no host
-- half and no block. This half adds the palette rows that start it plus an
-- `ssh://user@host:port` link route, and nothing else.
--
-- `tern-ssh` has to be on PATH in the pane's shell, or named by
-- $TERN_SSH_BIN. Build it with `make install` in the repository.

local IOS = tern.runtime.os == "ios"

-- POSIX single-quoting, for a path or host that reaches a shell command line.
local function quote(s: string): string
	return "'" .. string.gsub(s, "'", "'\\''") .. "'"
end

-- script is the command line that starts the manager.
local function script(): string
	local override = tern.getenv("TERN_SSH_BIN")
	if override ~= nil and override ~= "" then
		return quote(override)
	end
	return "tern-ssh"
end

-- A command cannot see which chord ran it, so each placement is its own row
-- and gets its own action id to bind: `plugin.ternssh.open` (a new tab) and
-- `plugin.ternssh.split` (a split of the focused pane). One chord runs one
-- action, so only `split` carries the default `cmd+shift+h`; bind the other
-- yourself in settings.json if you want it on a chord too.

-- start opens the manager in a new tab: it draws a full pane of its own, so it
-- gets the window's width instead of narrowing the pane you were in.
local function start(cx: WindowCx): number?
	return cx.layout:new_tab({ command = script() })
end

-- split puts the manager in a split of the focused pane; with no pane in focus
-- there is nothing to split, and it opens a tab instead.
local function split(cx: WindowCx): number?
	local focused = cx.session:focused()
	if focused == nil then
		return start(cx)
	end
	return cx.layout:split(focused, "right", { command = script() })
end

local ROWS = {
	{
		id = "open",
		title = "SSH hosts",
		icon = "server",
		start = start,
	},
	{
		id = "split",
		title = "SSH hosts split",
		icon = "server",
		keys = { "cmd+shift+h" },
		start = split,
	},
}

for _, row in ROWS do
	local start_it = row.start
	tern.command({
		id = row.id,
		title = row.title,
		icon = row.icon,
		group = "SSH",
		keys = row.keys,
		available = function(_cx: WindowCx): boolean
			return not IOS
		end,
		run = function(cx: WindowCx)
			start_it(cx)
		end,
	})
end

-- A clicked ssh:// link opens a session for that host, in a split of the pane
-- it was clicked in. The target is limited to the characters an ssh
-- destination may hold, so a link can never smuggle shell syntax into the
-- launch command line; anything else falls through to Tern's own handling of
-- the URL.
local function destination(url: string): string?
	local rest = string.match(url, "^ssh://(.+)$")
	if rest == nil then
		return nil
	end
	rest = string.gsub(rest, "[/?#].*$", "")
	return string.match(rest, "^[%w%.%-_@:]+$")
end

tern.route.link(function(link: LinkRequest, cx: WindowCx): RouteDecision?
	local host = destination(link.url)
	if host == nil then
		return nil
	end
	local focused = cx.session:focused()
	if focused == nil then
		return nil
	end
	cx.layout:split(focused, "right", { command = "ssh " .. quote(host) })
	return { handled = true }
end)
