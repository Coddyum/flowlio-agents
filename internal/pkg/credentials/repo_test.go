package credentials

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A saved repository comes back with the same address and the same secret, and the names come back
// normalised: what the caller reads is what composed the path.
func TestSaveRepoRoundTrips(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	path, err := SaveRepo(RepoFile{APIURL: "http://localhost:42058", Project: "Acme", Repo: "api", Token: "flw_secret"})
	if err != nil {
		t.Fatalf("SaveRepo: %v", err)
	}
	if filepath.Base(path) != "API.json" {
		t.Errorf("file = %s, expected API.json", filepath.Base(path))
	}
	if parent := filepath.Base(filepath.Dir(path)); parent != "acme" {
		t.Errorf("directory = %s, expected acme", parent)
	}

	// Looked up with the OTHER casing on purpose: `flowlio connect api` and `flowlio connect API`
	// name the same repository everywhere else, and two files here would make the second one written
	// the only one anything ever finds.
	f, err := LoadRepo("acme", "api")
	if err != nil {
		t.Fatalf("LoadRepo: %v", err)
	}
	if f.APIURL != "http://localhost:42058" || f.Token != "flw_secret" {
		t.Errorf("read back %+v", f)
	}
	if f.Project != "acme" || f.Repo != "API" {
		t.Errorf("names stored as %s/%s, expected acme/API", f.Project, f.Repo)
	}
}

// The token lives in clear on disk, so the file has to be unreadable by anybody else — the same
// guarantee credentials.json already carries.
func TestSaveRepoWritesRestrictivePermissions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	path, err := SaveRepo(RepoFile{APIURL: "http://localhost:42058", Project: "acme", Repo: "API", Token: "flw_secret"})
	if err != nil {
		t.Fatalf("SaveRepo: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != filePerm {
		t.Errorf("file mode = %o, expected %o", perm, filePerm)
	}

	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("Stat on the directory: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != dirPerm {
		t.Errorf("directory mode = %o, expected %o", perm, dirPerm)
	}
}

// A repository nothing has set up on this host is ErrNotFound, not a read error: it is the normal
// state before `flowlio setup`, and every caller branches on it.
func TestLoadRepoReportsAbsence(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if _, err := LoadRepo("acme", "API"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadRepo = %v, expected ErrNotFound", err)
	}
}

// The names come off a command line. One that composes a path out of the configuration directory is
// refused BEFORE anything is written — otherwise a typo writes a token somewhere else entirely.
func TestRepoPathRefusesNamesThatEscapeTheDirectory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cases := []struct{ project, repo string }{
		{"..", "API"},
		{"acme", ".."},
		{"acme/../..", "API"},
		{"acme", "../../etc/passwd"},
		{"", "API"},
		{"acme", ""},
	}

	for _, tc := range cases {
		if path, err := RepoPath(tc.project, tc.repo); err == nil {
			t.Errorf("RepoPath(%q, %q) yielded %s, expected a refusal", tc.project, tc.repo, path)
		}
	}
}

// ListRepos is what `flowlio setup --list` and `flowlio doctor` print. It orders by project then
// repo, and a host that has set nothing up yet is an empty list rather than an error.
func TestListRepos(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	empty, err := ListRepos()
	if err != nil {
		t.Fatalf("ListRepos on an untouched host: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("ListRepos yielded %d entries on an untouched host", len(empty))
	}

	for _, f := range []RepoFile{
		{APIURL: "http://localhost:42058", Project: "acme", Repo: "WEB", Token: "flw_1"},
		{APIURL: "http://localhost:42058", Project: "acme", Repo: "API", Token: "flw_2"},
		{APIURL: "http://localhost:42058", Project: "zinc", Repo: "CORE", Token: "flw_3"},
	} {
		if _, err := SaveRepo(f); err != nil {
			t.Fatalf("SaveRepo %s/%s: %v", f.Project, f.Repo, err)
		}
	}

	repos, err := ListRepos()
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}

	var got []string
	for _, f := range repos {
		got = append(got, f.Project+"/"+f.Repo)
	}
	if want := "acme/API acme/WEB zinc/CORE"; strings.Join(got, " ") != want {
		t.Errorf("ListRepos = %q, expected %q", strings.Join(got, " "), want)
	}
}

// One hand-edited file must not hide the repositories that are fine: the commands leaning on this
// list exist to say what is there.
func TestListReposSkipsAnUnreadableFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)

	if _, err := SaveRepo(RepoFile{APIURL: "http://localhost:42058", Project: "acme", Repo: "API", Token: "flw_1"}); err != nil {
		t.Fatalf("SaveRepo: %v", err)
	}
	broken := filepath.Join(home, "flowlio", "repos", "acme", "WEB.json")
	if err := os.WriteFile(broken, []byte("{ not json"), 0o600); err != nil {
		t.Fatalf("writing the broken file: %v", err)
	}

	repos, err := ListRepos()
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 1 || repos[0].Repo != "API" {
		t.Errorf("ListRepos = %+v, expected the API repository alone", repos)
	}
}
