package credentials

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                         | Ligne |
// |------------------|----------------------------------------------------------------|-------|
// | reposDir         | Directory holding every repository credential of this host      | 33    |
// | safeSegment      | Refuses a name that would compose a path out of that directory  | 47    |
// | RepoPath         | Path of one repository's credential file, names normalised      | 62    |
// | RepoRecordPath   | On-disk path of a full record: hosted keys by id, not by key    | 86    |
// | hostedRecordPath | Composes <project>/<repo-id>.json for a hosted record           | 97    |
// | normaliseProject | Lower-cases a project slug, the one spelling that is stored      | 116   |
// | normaliseRepo    | Upper-cases a repo key, the one spelling that is stored          | 118   |
//
// Fin du sommaire.
// =====================================================================
//
// WHERE A REPOSITORY'S FILE LIVES. One file per repository under repos/<project>/, keyed by the pair
// that identifies it — except a hosted record, which the machine holds no project slug for and so
// keys by the core id instead (RepoRecordPath). Normalisation is the one spelling rule: project slugs
// lower-case, repo keys upper-case, so a caller never has to remember which is which.

import (
	"fmt"
	"path/filepath"
	"strings"
)

// reposDirName is the sub-directory holding one file per repository.
const reposDirName = "repos"

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

// normaliseProject and normaliseRepo carry the one spelling rule of this package: a project slug is
// lower-case, a repo key is upper-case. Everything that composes a path or writes a file goes
// through them, so a caller never has to remember which is which.
func normaliseProject(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func normaliseRepo(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }
