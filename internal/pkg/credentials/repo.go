package credentials

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément     | Résumé                                                            | Ligne |
// |-------------|-------------------------------------------------------------------|-------|
// | RepoFile    | What one repository needs in order to reach its own board           | 60    |
// | reposDir    | Directory holding every repository credential of this host          | 76    |
// | safeSegment | Refuses a name that would compose a path out of that directory      | 90    |
// | RepoPath    | Path of one repository's credential file, names normalised          | 105   |
// | RepoRecordPath   | On-disk path of a full record: hosted keys by id, not by key   | 129   |
// | hostedRecordPath | Composes <project>/<repo-id>.json for a hosted record          | 140   |
// | LoadRepo    | Reads one repository's credential file                              | 158   |
// | SaveRepo    | Writes it in 0600, with its names normalised                        | 185   |
// | DeleteRepo  | Removes one, for a repository that no longer exists server-side     | 223   |
// | ListRepos   | Every repository credential on this host, project then repo         | 246   |
// | normaliseProject | Lower-cases a project slug, the one spelling that is stored    | 301   |
// | normaliseRepo    | Upper-cases a repo key, the one spelling that is stored        | 303   |
//
// Fin du sommaire.
// =====================================================================
//
// WHY THE PROJECT TOKEN LEAVES THE ENVIRONMENT.
//
// Until now a repository's `.mcp.json` carried `${FLOWLIO_TOKEN}`, and the token itself lived in
// the user's shell. Two repositories on one machine therefore fought over ONE variable name: the
// second one to be set up took a 401 and nothing said why. Exporting per directory is not a fix
// either — an agent launched from an editor inherits neither.
//
// So the secret moves here, keyed by the pair that identifies it: one file per repository, under
// the project it belongs to. The `.mcp.json` then carries names instead of a secret, which is what
// makes it committable AND makes two repositories on one machine work at the same time.
//
// The api_url travels with the token on purpose. It used to live in the committed `.mcp.json`,
// which froze the address a repository was initialised against: a repo set up against the Docker
// stack kept calling :42058 forever, even when the API had moved. Here it is host-local state,
// rewritten by `flowlio connect`, and no longer something a teammate inherits by cloning.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// reposDirName is the sub-directory holding one file per repository.
const reposDirName = "repos"

// RepoFile is what one repository needs in order to reach its own board: where the API is, who it
// is, and the secret that proves it.
//
// The last three fields serve the waker (DESIGN-WAKE §5, §7) and are host-local by nature — a
// filesystem path is not product data and never goes into a database. Path is the directory the
// agent is launched in, captured by `flowlio connect` from its working directory. Agent names the
// launch recipe (claude / codex / opencode); AgentCommand is the custom template for a tool no
// preset covers. All three are optional: an empty Agent means the Claude default.
type RepoFile struct {
	APIURL       string `json:"api_url"`
	Project      string `json:"project"`
	Repo         string `json:"repo"`
	Token        string `json:"token"`
	Path         string `json:"path,omitempty"`
	Agent        string `json:"agent,omitempty"`
	AgentCommand string `json:"agent_command,omitempty"`
	// RepoID is the core repository id a HOSTED waker polls the relay with
	// (`/api/v2/agents/wake?repo=<RepoID>`). It is empty in self-host, where the token and APIURL
	// point straight at the engine; it is set in hosted, where the token lives in flowlio-core and
	// this id is all the local machine holds to name the repository (DESIGN-WAKE §6).
	RepoID string `json:"repo_id,omitempty"`
}

// reposDir yields the directory holding every repository credential of this host.
func reposDir() (string, error) {
	base, err := dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, reposDirName), nil
}

// safeSegment refuses a name that would compose a path outside reposDir.
//
// The names come from a command line, and a repo key of `../../..` would otherwise have RepoPath
// hand a caller a path to somewhere else entirely — then have SaveRepo write a token there. The
// check is cheap and it is the only thing standing between a typo and a file written outside the
// configuration directory.
func safeSegment(kind, name string) error {
	if name == "" {
		return fmt.Errorf("credentials: %s is empty", kind)
	}
	if name != filepath.Base(name) || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("credentials: %s %q is not a plain name", kind, name)
	}
	return nil
}

// RepoPath yields $XDG_CONFIG_HOME/flowlio/repos/<project>/<REPO>.json.
//
// The repo key is upper-cased and the project slug lower-cased BEFORE the path is composed.
// Without it, `API.json` and `api.json` coexist on a case-sensitive filesystem while being the same
// repository everywhere else, and the second one written is the only one anything ever finds.
func RepoPath(project, repo string) (string, error) {
	project, repo = normaliseProject(project), normaliseRepo(repo)
	if err := safeSegment("project", project); err != nil {
		return "", err
	}
	if err := safeSegment("repo", repo); err != nil {
		return "", err
	}

	base, err := reposDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, project, repo+".json"), nil
}

// RepoRecordPath yields the on-disk path of a full record, which is NOT always <project>/<REPO>.json.
//
// A hosted record — one carrying a RepoID — is filed under that id, never under its key. A hosted
// machine holds no project slug: every hosted repo sits under the one "hosted" directory, so two
// projects that each called a repo CORE would otherwise write the SAME hosted/CORE.json, and the
// second `flowlio connect --id` would silently bury the first (seen in the field on 2026-08-12). The
// id is the one name a hosted machine has that is unique per repository, so it is what keys the file.
// A self-host record keeps the readable <project>/<REPO>.json, resolved through RepoPath.
func RepoRecordPath(f RepoFile) (string, error) {
	if f.RepoID != "" {
		return hostedRecordPath(f.Project, f.RepoID)
	}
	return RepoPath(f.Project, f.Repo)
}

// hostedRecordPath composes <project>/<repo-id>.json. The id is a UUID kept verbatim — never
// upper-cased the way a key is: a hosted record is read by content and never re-derived from its
// name, so the spelling only has to survive a filesystem round-trip, and safeSegment is the one check
// that still bears on it.
func hostedRecordPath(project, repoID string) (string, error) {
	project = normaliseProject(project)
	if err := safeSegment("project", project); err != nil {
		return "", err
	}
	if err := safeSegment("repo id", repoID); err != nil {
		return "", err
	}

	base, err := reposDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, project, repoID+".json"), nil
}

// LoadRepo reads one repository's credentials. Yields ErrNotFound when that repository has never
// been set up on this host — the normal case before `flowlio setup`, not a run-time error.
func LoadRepo(project, repo string) (RepoFile, error) {
	path, err := RepoPath(project, repo)
	if err != nil {
		return RepoFile{}, err
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RepoFile{}, ErrNotFound
		}
		return RepoFile{}, fmt.Errorf("credentials: reading %s: %w", path, err)
	}

	var f RepoFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return RepoFile{}, fmt.Errorf("credentials: %s unreadable: %w", path, err)
	}
	return f, nil
}

// SaveRepo writes one repository's credentials in 0600 inside 0700 directories, and yields the path
// written.
//
// The names are normalised INSIDE the file too, not only in its path: what a caller reads back is
// then exactly what composed the path, and the `.mcp.json` it writes from that carries the same
// spelling the MCP server will look up.
func SaveRepo(f RepoFile) (string, error) {
	f.Project, f.Repo = normaliseProject(f.Project), normaliseRepo(f.Repo)

	path, err := RepoRecordPath(f)
	if err != nil {
		return "", err
	}
	// A hosted record (it carries the core repo id) holds neither address nor token: both live in
	// hosted.json, and the waker reads them from there. What it needs here is the id and the path.
	// A self-host record is the opposite — it dials the engine directly, so both are required.
	if f.RepoID != "" {
		if f.Path == "" {
			return "", fmt.Errorf("credentials: repo %s/%s: a hosted record needs a path", f.Project, f.Repo)
		}
	} else if f.APIURL == "" || f.Token == "" {
		return "", fmt.Errorf("credentials: repo %s/%s: address and token are both required", f.Project, f.Repo)
	}

	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return "", fmt.Errorf("credentials: creating %s: %w", filepath.Dir(path), err)
	}

	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return "", fmt.Errorf("credentials: encoding: %w", err)
	}

	if err := os.WriteFile(path, append(raw, '\n'), filePerm); err != nil {
		return "", fmt.Errorf("credentials: writing %s: %w", path, err)
	}
	return path, nil
}

// DeleteRepo removes one repository's credential file, and says whether there was one to remove.
//
// It exists for `flowlio remove`: a repository deleted server-side leaves a token here that
// authenticates nothing, and a credential outliving what it opens is how a host accumulates secrets
// nobody can account for.
func DeleteRepo(project, repo string) (removed bool, err error) {
	path, err := RepoPath(project, repo)
	if err != nil {
		return false, err
	}

	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("credentials: removing %s: %w", path, err)
	}
	return true, nil
}

// ListRepos yields every repository credential of this host, ordered by project then repo.
//
// An absent directory is an empty list, not an error: nothing has been set up yet, which is a state
// `flowlio setup --list` and `flowlio doctor` both have to be able to report calmly.
//
// A file that does not decode is SKIPPED rather than fatal. This list serves commands whose whole
// job is to say what is there; failing the lot because one file was hand-edited would hide the
// other repositories, which are fine.
func ListRepos() ([]RepoFile, error) {
	base, err := reposDir()
	if err != nil {
		return nil, err
	}

	projects, err := os.ReadDir(base)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("credentials: reading %s: %w", base, err)
	}

	var out []RepoFile
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(base, project.Name()))
		if err != nil {
			return nil, fmt.Errorf("credentials: reading %s: %w", filepath.Join(base, project.Name()), err)
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			// Read the file by its own bytes rather than re-derive a key from its name and hand it back
			// to LoadRepo: a hosted record is filed under its id, and LoadRepo would upper-case that id
			// into a name no file carries. The record's Project/Repo fields are the authority here, not
			// the filename — which is exactly why a hosted id in the filename changes nothing downstream.
			raw, err := os.ReadFile(filepath.Join(base, project.Name(), entry.Name()))
			if err != nil {
				continue
			}
			var f RepoFile
			if err := json.Unmarshal(raw, &f); err != nil {
				continue
			}
			out = append(out, f)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].Repo < out[j].Repo
	})
	return out, nil
}

// normaliseProject and normaliseRepo carry the one spelling rule of this package: a project slug is
// lower-case, a repo key is upper-case. Everything that composes a path or writes a file goes
// through them, so a caller never has to remember which is which.
func normaliseProject(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func normaliseRepo(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }
