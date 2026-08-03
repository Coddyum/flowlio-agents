package service

// Ce que ce fichier verrouille : la seule chose que le service décide sur le graphe, c'est-à-dire
// la validation de deux chaînes tapées par un humain.
//
// Tout le reste — l'appartenance des projets à la team, l'autorisation elle-même — vit dans le
// SQL et n'est pas testable ici, délibérément. Un test de service qui prouverait la tenancy
// prouverait la tenancy DU FAKE.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/store"
	"github.com/google/uuid"
)

// trustSpy enregistre les clés reçues par le store, telles que le service les a normalisées.
// C'est la seule façon de vérifier que la normalisation a bien lieu AVANT l'appel — un fake qui
// normaliserait à son tour rendrait le test vert quelle que soit l'implémentation.
type trustSpy struct {
	store.Store

	calls  int
	first  string
	second string
}

func (s *trustSpy) AllowTrust(_ context.Context, _ uuid.UUID, first, second string) (bool, error) {
	s.calls++
	s.first, s.second = first, second
	return true, nil
}

func (s *trustSpy) RevokeTrust(_ context.Context, _ uuid.UUID, first, second string) (bool, error) {
	s.calls++
	s.first, s.second = first, second
	return true, nil
}

// Un projet ne peut pas s'autoriser lui-même, et le message le DIT.
//
// La base refuserait de toute façon (project_trust_ordered exclut l'égalité), mais elle rendrait
// un `not found` — c'est-à-dire, pour l'humain, le même message que s'il avait tapé une clé qui
// n'existe pas. Ce contrôle existe pour transformer ce silence en phrase utile.
//
// Le cas `frnt`/`FRNT` est celui qui compte : la comparaison a lieu APRÈS normalisation. Sans ça,
// la validation passerait et la base rendrait un 404 sur une commande dont la faute était
// évidente.
func TestTrustRefusesASelfPair(t *testing.T) {
	teamID := uuid.New()

	cas := []struct{ name, first, second string }{
		{"clés identiques", "FRNT", "FRNT"},
		{"casse différente", "frnt", "FRNT"},
		{"espaces autour", " FRNT ", "FRNT"},
	}

	for _, c := range cas {
		t.Run(c.name, func(t *testing.T) {
			for verb, call := range map[string]func(Service, TrustPairInput) error{
				"allow": func(s Service, in TrustPairInput) error { _, err := s.AllowTrust(context.Background(), in); return err },
				"deny":  func(s Service, in TrustPairInput) error { _, err := s.RevokeTrust(context.Background(), in); return err },
			} {
				spy := &trustSpy{}
				err := call(New(spy), TrustPairInput{TeamID: teamID, First: c.first, Second: c.second})

				if !errors.Is(err, ErrInvalidInput) {
					t.Errorf("%s: erreur = %v, attendu ErrInvalidInput", verb, err)
				}
				if !strings.Contains(err.Error(), "lui-même") {
					t.Errorf("%s: message = %q, attendu une phrase qui explique le refus", verb, err)
				}
				if spy.calls != 0 {
					t.Errorf("%s: le store a été appelé %d fois — le refus arrive après le travail", verb, spy.calls)
				}
			}
		})
	}
}

// Les clés arrivent au store en MAJUSCULES et sans espaces. `frnt` et `FRNT` désignent le même
// projet : laisser la casse décider de l'existence d'une arête produirait deux graphes pour une
// seule intention, dont un que personne ne relit.
//
// MUTATION : retirer `strings.ToUpper` de normalisePair fait tomber ce test.
func TestTrustNormalisesKeysBeforeTheStore(t *testing.T) {
	teamID := uuid.New()

	spy := &trustSpy{}
	if _, err := New(spy).AllowTrust(context.Background(), TrustPairInput{
		TeamID: teamID, First: " frnt ", Second: "core",
	}); err != nil {
		t.Fatalf("AllowTrust: %v", err)
	}

	if spy.first != "FRNT" || spy.second != "CORE" {
		t.Errorf("le store a reçu (%q, %q), attendu (\"FRNT\", \"CORE\")", spy.first, spy.second)
	}
}

// L'ORDRE des deux clés est conservé tel quel jusqu'au store : le service ne trie pas.
//
// C'est délibéré et ça mérite un test, parce que l'intuition dit l'inverse. La normalisation en
// paire canonique (`least`/`greatest`) a lieu DANS LA QUERY, sur les UUID — pas sur les clés. Un
// tri par clé ici serait un second ordre canonique, faux : `least` porte sur les identifiants,
// dont l'ordre n'a aucun rapport avec l'alphabet.
func TestTrustDoesNotReorderKeys(t *testing.T) {
	teamID := uuid.New()

	spy := &trustSpy{}
	if _, err := New(spy).AllowTrust(context.Background(), TrustPairInput{
		TeamID: teamID, First: "ZULU", Second: "ALFA",
	}); err != nil {
		t.Fatalf("AllowTrust: %v", err)
	}

	if spy.first != "ZULU" || spy.second != "ALFA" {
		t.Errorf("le store a reçu (%q, %q), attendu l'ordre de la commande — la paire canonique "+
			"se calcule sur les UUID dans la query, pas sur les clés ici", spy.first, spy.second)
	}
}

// Une clé malformée est refusée avant d'atteindre le store, par le même validateur que partout.
func TestTrustRejectsMalformedKeys(t *testing.T) {
	teamID := uuid.New()

	for _, key := range []string{"", "F", "frnt-web", "1FRNT", "TROPLONGUECLE", "FR NT"} {
		t.Run("clé "+key, func(t *testing.T) {
			spy := &trustSpy{}
			if _, err := New(spy).AllowTrust(context.Background(), TrustPairInput{
				TeamID: teamID, First: key, Second: "CORE",
			}); !errors.Is(err, ErrInvalidInput) {
				t.Errorf("erreur = %v, attendu ErrInvalidInput", err)
			}
			if spy.calls != 0 {
				t.Errorf("le store a été appelé malgré une clé invalide")
			}
		})
	}
}

// Sans team résolue, rien ne part. C'est le garde-fou du câblage : si un handler oubliait
// d'affecter TeamID après teamFor, l'écriture partirait sous uuid.Nil — donc sous aucune team,
// donc en `not found` silencieux plutôt qu'en erreur de programmation visible.
func TestTrustRefusesAnUnresolvedTeam(t *testing.T) {
	spy := &trustSpy{}

	if _, err := New(spy).AllowTrust(context.Background(), TrustPairInput{
		First: "FRNT", Second: "CORE",
	}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("AllowTrust sans team: erreur = %v, attendu ErrInvalidInput", err)
	}
	if _, err := New(spy).ListTrust(context.Background(), uuid.Nil); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("ListTrust sans team: erreur = %v, attendu ErrInvalidInput", err)
	}
	if spy.calls != 0 {
		t.Errorf("le store a été appelé sans team résolue")
	}
}
