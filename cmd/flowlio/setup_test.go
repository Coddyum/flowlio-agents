package main

import (
	"io/fs"
	"strings"
	"testing"
	"time"
)

// AN AGENT RUNS THIS WITH NO TERMINAL. A question asked there is a session that hangs until
// somebody kills it, so the command refuses — and the refusal names the flag form, because an error
// that does not say what to do instead is a dead end.
//
// MUTATION: drop the isInteractive guard — this hangs instead of going red, which is exactly the
// failure being guarded against.
func TestSetupRefusesToAskWhenNobodyIsThere(t *testing.T) {
	var out strings.Builder

	_, err := askSetupPlan(strings.NewReader(""), &out, "", "", nil)
	if err == nil {
		t.Fatal("a plan was composed with nothing to read from")
	}
	for _, want := range []string{"--project", "--repo"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
}

// The flag form composes the same plan the conversation does, and it asks nothing on the way.
func TestSetupPlanFromFlags(t *testing.T) {
	var repos repoFlags
	for _, value := range []string{"API:acme-api", "web"} {
		if err := repos.Set(value); err != nil {
			t.Fatalf("--repo %s: %v", value, err)
		}
	}

	var out strings.Builder
	plan, err := askSetupPlan(strings.NewReader(""), &out, "acme", "", repos)
	if err != nil {
		t.Fatalf("askSetupPlan: %v", err)
	}

	if plan.slug != "acme" || plan.name != "acme" {
		t.Errorf("plan = %s/%s, expected acme/acme (the name defaults to the slug)", plan.slug, plan.name)
	}
	if len(plan.repos) != 2 {
		t.Fatalf("plan carries %d repos, expected 2", len(plan.repos))
	}
	// `--repo web` is a repository called WEB: the key is upper-cased, and with no name it is its
	// own name.
	if plan.repos[1].Key != "WEB" || plan.repos[1].Name != "WEB" {
		t.Errorf("second repo = %+v, expected WEB/WEB", plan.repos[1])
	}
	if out.String() != "" {
		t.Errorf("the flag form printed a question:\n%s", out.String())
	}
}

// Half the flags is not a plan. Creating a project with no repository would leave the user with
// nothing to connect and no error to read.
func TestSetupRefusesHalfTheFlags(t *testing.T) {
	var out strings.Builder
	if _, err := askSetupPlan(strings.NewReader(""), &out, "acme", "", nil); err == nil {
		t.Error("--project alone was accepted")
	}

	var repos repoFlags
	if err := repos.Set("API"); err != nil {
		t.Fatalf("--repo API: %v", err)
	}
	if _, err := askSetupPlan(strings.NewReader(""), &out, "", "", repos); err == nil {
		t.Error("--repo alone was accepted")
	}
}

// The same key twice is refused rather than silently deduplicated: the user meant to name two
// repositories, and one of the two names is wrong.
func TestRepoFlagsRefuseADuplicateKey(t *testing.T) {
	var repos repoFlags
	if err := repos.Set("API:acme-api"); err != nil {
		t.Fatalf("first --repo: %v", err)
	}
	if err := repos.Set("api:something-else"); err == nil {
		t.Error("the same key was accepted twice")
	}
}

// The key rules are the server's, checked here so a typo is refused at the keyboard rather than
// after a round trip that has already created a project.
func TestValidateRepoKeyMirrorsTheServer(t *testing.T) {
	valid := []string{"API", "WEB", "CORE2", "AB", "ABCDEFGHIJ"}
	invalid := []string{"", "A", "api", "2API", "A-B", "ABCDEFGHIJK", "A_B", "APÎ"}

	for _, key := range valid {
		if err := validateRepoKey(key); err != nil {
			t.Errorf("%q was refused: %v", key, err)
		}
	}
	for _, key := range invalid {
		if err := validateRepoKey(key); err == nil {
			t.Errorf("%q was accepted", key)
		}
	}
}

// The slug is DERIVED and then offered. What it derives has to be valid, or the offer is a trap:
// the user presses Enter on a suggestion the server then refuses.
func TestSlugifyProducesAValidSlug(t *testing.T) {
	cases := map[string]string{
		"acme":                 "acme",
		"Acme Corp":            "acme-corp",
		"  Acme   Corp  ":      "acme-corp",
		"Acme's Widgets, Inc.": "acme-s-widgets-inc",
		"ACME/2":               "acme-2",
		"---acme---":           "acme",
	}

	for name, want := range cases {
		got := slugify(name)
		if got != want {
			t.Errorf("slugify(%q) = %q, expected %q", name, got, want)
		}
		if err := validateProjectSlug(got); err != nil {
			t.Errorf("slugify(%q) produced %q, which the server would refuse: %v", name, got, err)
		}
	}
}

// The interactive path composes a plan from typed answers, and an empty line takes the offer. This
// is also what proves the prompter reads one conversation rather than one reader per question: six
// answers arrive in a single pipe.
func TestSetupPlanFromAConversation(t *testing.T) {
	answers := strings.Join([]string{
		"Acme Corp", // project name
		"",          // slug: take the derived one
		"API",       // first repo key
		"acme-api",  // its name
		"y",         // add another
		"WEB",       // second repo key
		"",          // its name: take the key
		"n",         // no more
	}, "\n") + "\n"

	var out strings.Builder
	plan, err := askSetupPlan(alwaysInteractive{strings.NewReader(answers)}, &out, "", "", nil)
	if err != nil {
		t.Fatalf("askSetupPlan: %v", err)
	}

	if plan.slug != "acme-corp" || plan.name != "Acme Corp" {
		t.Errorf("plan = %q/%q, expected acme-corp/Acme Corp", plan.slug, plan.name)
	}
	if len(plan.repos) != 2 {
		t.Fatalf("plan carries %d repos, expected 2:\n%s", len(plan.repos), out.String())
	}
	if plan.repos[0].Key != "API" || plan.repos[0].Name != "acme-api" {
		t.Errorf("first repo = %+v, expected API/acme-api", plan.repos[0])
	}
	if plan.repos[1].Key != "WEB" || plan.repos[1].Name != "WEB" {
		t.Errorf("second repo = %+v, expected WEB/WEB", plan.repos[1])
	}
}

// An invalid key is re-asked rather than sent to the server. The second answer is the one that
// counts, and the reason is printed in between.
func TestSetupReAsksAnInvalidKey(t *testing.T) {
	answers := "acme\n\nnope-not-a-key\nAPI\n\nn\n"

	var out strings.Builder
	plan, err := askSetupPlan(alwaysInteractive{strings.NewReader(answers)}, &out, "", "", nil)
	if err != nil {
		t.Fatalf("askSetupPlan: %v", err)
	}
	if len(plan.repos) != 1 || plan.repos[0].Key != "API" {
		t.Fatalf("plan carries %+v, expected the single repo API", plan.repos)
	}
	if !strings.Contains(out.String(), "uppercase letters and digits") {
		t.Errorf("the rule was not shown when the key was refused:\n%s", out.String())
	}
}

// alwaysInteractive makes a plain reader look like the terminal isInteractive is looking for, so
// the conversation can be driven from a test. isInteractive itself is covered in adopt_test.go,
// against the real thing — including /dev/null, which is the case a fake cannot represent.
type alwaysInteractive struct{ *strings.Reader }

func (alwaysInteractive) Stat() (fs.FileInfo, error) { return charDevice{}, nil }

// charDevice is the smallest FileInfo that reads as a terminal.
type charDevice struct{}

func (charDevice) Name() string       { return "fake-tty" }
func (charDevice) Size() int64        { return 0 }
func (charDevice) Mode() fs.FileMode  { return fs.ModeCharDevice }
func (charDevice) ModTime() time.Time { return time.Time{} }
func (charDevice) IsDir() bool        { return false }
func (charDevice) Sys() any           { return nil }
