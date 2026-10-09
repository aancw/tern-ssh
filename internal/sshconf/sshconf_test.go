package sshconf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture is a config with the shapes tern-ssh must survive: global
// directives, wildcard and Match blocks, aliases sharing a block, `key=value`
// spelling, comments, an Include line and two written blocks.
const fixture = `# my ssh config
HashKnownHosts yes

Host *
	AddKeysToAgent yes
	IdentityFile ~/.ssh/id_ed25519

# the build box
Host build build2
	HostName 10.0.0.9
	User ci
	Port 2200

Host jump
	HostName jump.example.com
	User root

Host db
	HostName db.internal
	ProxyJump=jump

Match host *.example.com
	User admin

Include ~/.ssh/config.d/*

# tern-ssh
Host edge
	HostName edge.internal
	User deploy
	IdentityFile ~/.ssh/edge
`

func write(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func load(t *testing.T, path string) *File {
	t.Helper()
	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return f
}

func TestLoadParsesAliases(t *testing.T) {
	f := load(t, write(t, "config", fixture))
	got := f.Names()
	want := []string{"build", "build2", "jump", "db", "edge"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("aliases = %v, want %v", got, want)
	}
}

func TestLoadResolvesOptions(t *testing.T) {
	f := load(t, write(t, "config", fixture))

	build := f.Find("build")
	if build == nil {
		t.Fatal("build not found")
	}
	if build.HostName() != "10.0.0.9" || build.User() != "ci" || build.Port() != "2200" {
		t.Fatalf("build = %s", build.Target())
	}
	if build.Target() != "ci@10.0.0.9:2200" {
		t.Fatalf("target = %q", build.Target())
	}
	// Host * holds defaults the alias itself does not set; they resolve.
	if build.IdentityFile() != "~/.ssh/id_ed25519" {
		t.Fatalf("identity = %q", build.IdentityFile())
	}
	if build.Managed {
		t.Fatal("build must not be marked as written by tern-ssh")
	}

	// A second alias of the same block sees the same options.
	if build2 := f.Find("build2"); build2 == nil || build2.HostName() != "10.0.0.9" {
		t.Fatalf("build2 = %+v", build2)
	}
	// `key=value` spelling parses like `key value`.
	if db := f.Find("db"); db == nil || db.HostName() != "db.internal" || db.ProxyJump() != "jump" {
		t.Fatalf("db = %+v", db)
	}
	edge := f.Find("edge")
	if !edge.Managed || edge.User() != "deploy" {
		t.Fatalf("edge = %+v", edge)
	}
}

func TestFirstValueWins(t *testing.T) {
	f := load(t, write(t, "config", "Host a\n\tUser first\n\tUser second\n\nHost b\n\tUser third\n"))
	if got := f.Find("a").User(); got != "first" {
		t.Fatalf("User = %q, want first", got)
	}
}

func TestAddWritesOnlyItsOwnLines(t *testing.T) {
	path := write(t, "config", fixture)
	f := load(t, path)
	if err := f.Add(NewEntry("newbox", "10.0.0.20", "ubuntu", "22", "~/.ssh/new", "")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if !strings.HasPrefix(text, fixture) {
		t.Fatalf("Add changed the file before its own block:\n%s", text)
	}
	want := "\n" + Marker + "\nHost newbox\n\tHostName 10.0.0.20\n\tUser ubuntu\n\tPort 22\n\tIdentityFile ~/.ssh/new\n"
	if !strings.HasSuffix(text, want) {
		t.Fatalf("appended block = %q, want %q", text[len(fixture):], want)
	}
	if e := load(t, path).Find("newbox"); e == nil || !e.Managed || e.Port() != "22" {
		t.Fatalf("reloaded entry = %+v", e)
	}
}

func TestAddRejectsDuplicatesAndBadValues(t *testing.T) {
	f := load(t, write(t, "config", fixture))
	if err := f.Add(NewEntry("jump", "", "", "", "", "")); err == nil {
		t.Fatal("Add accepted an existing alias")
	}
	if err := f.Add(NewEntry("bad port", "", "", "", "", "")); err == nil {
		t.Fatal("Add accepted a spaced alias")
	}
	if err := f.Add(NewEntry("p", "", "", "70000", "", "")); err == nil {
		t.Fatal("Add accepted an out-of-range port")
	}
	if err := f.Add(NewEntry("u", "", "a b", "", "", "")); err == nil {
		t.Fatal("Add accepted a spaced user")
	}
}

func TestUpdateRewritesOnlyTheBlock(t *testing.T) {
	path := write(t, "config", fixture)
	f := load(t, path)
	before := strings.Split(fixture, "\n")

	e := *f.Find("jump")
	e.Options = NewEntry("jump", "jump2.example.com", "admin", "2222", "", "").Options
	if err := f.Update(e.Name, e); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join(before[:13], "\n") + "\n" +
		"Host jump\n\tHostName jump2.example.com\n\tUser admin\n\tPort 2222\n" +
		strings.Join(before[16:], "\n")
	if string(got) != want {
		t.Fatalf("file =\n%q\nwant\n%q", got, want)
	}
	if after := load(t, path).Find("jump"); after == nil || after.User() != "admin" || after.Port() != "2222" {
		t.Fatalf("reloaded jump = %+v", after)
	}
	if other := load(t, path).Find("build"); other == nil || other.HostName() != "10.0.0.9" {
		t.Fatalf("reloaded build = %+v", other)
	}
}

func TestUpdateKeepsCommentsInsideTheBlock(t *testing.T) {
	path := write(t, "config", "Host a\n\t# keep me\n\tUser bob\n\nHost b\n\tUser c\n")
	f := load(t, path)
	e := *f.Find("a")
	e.Options = NewEntry("a", "host.a", "", "", "", "").Options
	if err := f.Update(e.Name, e); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := os.ReadFile(path)
	want := "Host a\n\t# keep me\n\tHostName host.a\n\nHost b\n\tUser c\n"
	if string(got) != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestRemoveDropsBlockMarkerAndGap(t *testing.T) {
	path := write(t, "config", fixture)
	f := load(t, path)
	if err := f.Remove("edge"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "edge") || strings.Contains(string(got), Marker) {
		t.Fatalf("Remove left the block behind:\n%s", got)
	}
	if !strings.HasSuffix(string(got), "Include ~/.ssh/config.d/*\n") {
		t.Fatalf("Remove ate the tail:\n%q", got)
	}
	if e := load(t, path).Find("edge"); e != nil {
		t.Fatal("edge survived a reload")
	}
}

func TestMissingFileIsEmptyAndWritable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config")
	f := load(t, path)
	if len(f.Entries) != 0 {
		t.Fatalf("entries = %v", f.Entries)
	}
	if err := f.Add(NewEntry("solo", "solo.example.com", "", "", "", "")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := Marker + "\nHost solo\n\tHostName solo.example.com\n"
	if string(got) != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestSaveKeepsPermissions(t *testing.T) {
	path := write(t, "config", "Host a\n")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	f := load(t, path)
	if err := f.Add(NewEntry("b", "", "", "", "", "")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v, want 0644", info.Mode().Perm())
	}
}

func TestWildcardBlocksAreNotEntries(t *testing.T) {
	f := load(t, write(t, "config", "Host *\n\tUser root\n\nHost !excluded\n\tUser nobody\n"))
	if len(f.Entries) != 0 {
		t.Fatalf("entries = %v, want none", f.Names())
	}
}

func TestDefaultPathHonorsEnv(t *testing.T) {
	t.Setenv("TERN_SSH_CONFIG", "~/elsewhere/config")
	home, _ := os.UserHomeDir()
	if got := DefaultPath(); got != filepath.Join(home, "elsewhere", "config") {
		t.Fatalf("DefaultPath = %q", got)
	}
}

func TestUpdateKeepsUnmanagedOptions(t *testing.T) {
	path := write(t, "config", "Host a\n\tHostName old.example.com\n\tForwardAgent yes\n\tLocalForward 8080 localhost:80\n")
	f := load(t, path)
	e := *f.Find("a")
	e.Options = NewEntry("a", "new.example.com", "root", "", "", "").Options
	if err := f.Update(e.Name, e); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := os.ReadFile(path)
	want := "Host a\n\tHostName new.example.com\n\tUser root\n\tForwardAgent yes\n\tLocalForward 8080 localhost:80\n"
	if string(got) != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestUpdateRenamesTheBlock(t *testing.T) {
	path := write(t, "config", "Host web\n\tHostName 1.2.3.4\n\tUser root\n\nHost db\n\tHostName 2.2.2.2\n")
	f := load(t, path)
	e := *f.Find("web")
	e.Name = "front"
	if err := f.Update("web", e); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := os.ReadFile(path)
	want := "Host front\n\tHostName 1.2.3.4\n\tUser root\n\nHost db\n\tHostName 2.2.2.2\n"
	if string(got) != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
	after := load(t, path)
	if after.Find("front") == nil || after.Find("web") != nil {
		t.Fatalf("aliases = %v", after.Names())
	}
	// Renaming onto a name that is taken is refused, and changes nothing.
	e2 := *after.Find("front")
	e2.Name = "db"
	if err := after.Update("front", e2); err == nil {
		t.Fatal("Update renamed onto an existing host")
	}
	if again, _ := os.ReadFile(path); string(again) != want {
		t.Fatalf("refused rename changed the file: %q", again)
	}
}

func TestUpdateKeepsGapCommentsInPlace(t *testing.T) {
	const file = "Host web\n\tHostName 1.2.3.4\n\n# db server\nHost db\n\tHostName 2.2.2.2\n"
	path := write(t, "config", file)
	f := load(t, path)
	e := *f.Find("web")
	e.Options = NewEntry("web", "5.6.7.8", "", "", "", "").Options
	if err := f.Update("web", e); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := os.ReadFile(path)
	want := "Host web\n\tHostName 5.6.7.8\n\n# db server\nHost db\n\tHostName 2.2.2.2\n"
	if string(got) != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
	if strings.Count(string(got), "# db server") != 1 {
		t.Fatalf("the gap comment was duplicated:\n%s", got)
	}
}

func TestUpdateKeepsCommentsInsideTheBlockOnly(t *testing.T) {
	const file = "Host web\n\t# inside\n\tHostName 1.2.3.4\n# above db\nHost db\n"
	path := write(t, "config", file)
	f := load(t, path)
	e := *f.Find("web")
	e.Options = NewEntry("web", "9.9.9.9", "", "", "", "").Options
	if err := f.Update("web", e); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := os.ReadFile(path)
	want := "Host web\n\t# inside\n\tHostName 9.9.9.9\n# above db\nHost db\n"
	if string(got) != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}
