package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | runTrust           | Sous-commandes d'édition du graphe de confiance             | 46    |
// | trustList          | Affiche le graphe, ou dit quoi taper s'il est vide          | 93    |
// | trustAllow         | Ouvre une paire et le confirme                              | 128   |
// | trustDeny          | Ferme une paire, en nommant le vrai coupe-circuit           | 150   |
// | trustPath          | Compose le chemin d'une route trust avec sa team            | 172   |
// | explainAdminToken  | Traduit un 403 en conseil sur le bon token                  | 186   |
// | possiblePairs      | Nombre de paires possibles dans une team de n projets       | 200   |
// | joinKeys           | Liste les clés de projets, séparées par des virgules         | 208   |
// | teamOption         | Rend l'option --team à recopier dans la commande suggérée    | 218   |
//
// Fin du sommaire.
// =====================================================================
//
// `flowlio trust` est la SEULE surface où la vérité du graphe est lisible, et la première commande
// que tape un humain dont un agent vient de recevoir `not found` sur un create_issue. Elle est
// donc écrite pour être lue par quelqu'un qui ne sait pas encore ce qui lui arrive.
//
// Aucune de ces trois commandes n'existe côté MCP, et c'est la décision : un agent SUBIT le
// graphe. Lui donner de quoi le lire lui donnerait la carte de ce qu'il peut atteindre ; lui
// donner de quoi l'écrire lui laisserait s'auto-signer une autorisation.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
)

// runTrust route les trois sous-commandes du graphe de confiance.
//
// `deny` et non `revoke` : `token revoke` existe déjà et coupe TOUT, tout de suite. Deux verbes
// identiques pour deux gestes dont l'un confine et l'autre pas seraient confondus le jour d'un
// incident, c'est-à-dire le seul jour où ça compte.
func runTrust(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: flowlio trust list | allow <A> <B> | deny <A> <B> [--team slug]")
	}

	fs := flag.NewFlagSet("trust", flag.ContinueOnError)
	team := teamFlag(fs)

	sub := args[0]
	positional, err := splitFlags(fs, args[1:])
	if err != nil {
		return err
	}

	c, err := newClient()
	if err != nil {
		return err
	}

	switch sub {
	case "list":
		return explainAdminToken(trustList(ctx, c, *team))

	case "allow":
		if len(positional) < 2 {
			return errors.New("usage: flowlio trust allow <A> <B> [--team slug]")
		}
		return explainAdminToken(trustAllow(ctx, c, *team, positional[0], positional[1]))

	case "deny":
		if len(positional) < 2 {
			return errors.New("usage: flowlio trust deny <A> <B> [--team slug]")
		}
		return explainAdminToken(trustDeny(ctx, c, *team, positional[0], positional[1]))

	default:
		return fmt.Errorf("sous-commande trust inconnue: %s", sub)
	}
}

// trustList affiche le graphe d'une team.
//
// Un graphe VIDE est le cas le plus important de toute cette commande : c'est l'état par défaut de
// toute team après la migration, donc l'état dans lequel un humain arrive après qu'un agent lui a
// dit « not found ». La sortie ne se contente pas de ne rien afficher — elle nomme les projets et
// donne la commande exacte à taper. Aucun oracle : cette route est admin, et un admin peut déjà
// énumérer tous les projets de toutes les teams.
func trustList(ctx context.Context, c *client.Client, team string) error {
	var edges []service.TrustEdge
	if err := c.Do(ctx, http.MethodGet, trustPath(team, ""), nil, &edges); err != nil {
		return err
	}

	var projects []service.Project
	if err := c.Do(ctx, http.MethodGet, workspaceAPI+"/projects"+teamQuery(team), nil, &projects); err != nil {
		return err
	}

	if len(edges) == 0 {
		fmt.Println("\n  aucune confiance déclarée — le canal inter-projets est fermé pour cette team.")
		if len(projects) < 2 {
			fmt.Println("  cette team compte moins de deux projets : il n'y a aucune paire possible.")
			return nil
		}
		fmt.Printf("  projets : %s\n", joinKeys(projects))
		fmt.Printf("  ouvrir une paire :  flowlio trust allow %s %s%s\n\n",
			projects[0].Key, projects[1].Key, teamOption(team))
		return nil
	}

	fmt.Println()
	for _, e := range edges {
		fmt.Printf("  %s ↔ %-12s depuis le %s\n", e.First, e.Second, e.CreatedAt.Format("2006-01-02"))
	}

	total := possiblePairs(len(projects))
	fmt.Printf("\n  %d paire(s) sur %d possible(s).\n\n", len(edges), total)
	return nil
}

// trustAllow ouvre une paire. Idempotente, et le dit : un rejeu n'est pas une erreur, mais
// laisser croire à l'humain qu'il vient de changer quelque chose en serait une.
func trustAllow(ctx context.Context, c *client.Client, team, first, second string) error {
	var decision service.TrustDecision
	in := service.TrustPairInput{First: first, Second: second}
	if err := c.Do(ctx, http.MethodPost, trustPath(team, ""), in, &decision); err != nil {
		return err
	}

	if !decision.Changed {
		fmt.Printf("%s ↔ %s : déjà autorisés, rien à faire.\n", decision.First, decision.Second)
		return nil
	}
	fmt.Printf("%s ↔ %s : les deux projets peuvent désormais s'adresser des issues.\n",
		decision.First, decision.Second)
	return nil
}

// trustDeny ferme une paire.
//
// LES TROIS DERNIÈRES LIGNES DE CETTE SORTIE SONT LES PLUS IMPORTANTES DE LA COMMANDE. Elles
// disent que `trust deny` n'est PAS un outil de confinement, et nomment celui qui l'est. Sans
// elles, un humain qui vient de découvrir qu'un repo est compromis croirait l'avoir coupé alors
// que chaque fil déjà ouvert reste répondable, sans borne de temps.
func trustDeny(ctx context.Context, c *client.Client, team, first, second string) error {
	path := trustPath(team, url.PathEscape(first)+"/"+url.PathEscape(second))

	var decision service.TrustDecision
	if err := c.Do(ctx, http.MethodDelete, path, nil, &decision); err != nil {
		return err
	}

	if !decision.Changed {
		fmt.Printf("%s ↔ %s : aucune confiance déclarée, rien à retirer.\n", decision.First, decision.Second)
		return nil
	}

	fmt.Printf("%s ↔ %s : confiance retirée. Aucune nouvelle issue entre ces deux projets.\n",
		decision.First, decision.Second)
	fmt.Println("Les fils déjà ouverts restent lisibles et répondables.")
	fmt.Println("Pour couper immédiatement un repo compromis : flowlio token revoke <id>.")
	return nil
}

// trustPath compose le chemin d'une route trust. suffix porte les deux clés du DELETE, vide
// ailleurs — les clés valident ^[A-Z][A-Z0-9]{1,9}$, donc elles sont sûres en segment d'URL.
func trustPath(team, suffix string) string {
	path := workspaceAPI + "/trust"
	if suffix != "" {
		path += "/" + suffix
	}
	return path + teamQuery(team)
}

// explainAdminToken transforme un 403 nu en conseil.
//
// Sans ça, l'humain qui vient de suivre `flowlio init` et d'exporter FLOWLIO_TOKEN reçoit
// `flowlio: api: Forbidden`, une commande après qu'on lui a fait exporter le mauvais token. C'est
// le seul endroit du produit où un 403 est attendu SUR UNE ERREUR DE MANIPULATION plutôt que sur
// une tentative : le message dit donc quoi faire, pas ce qui est interdit.
func explainAdminToken(err error) error {
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
		return err
	}
	return fmt.Errorf(`%w
        cette commande demande le token d'ADMINISTRATION, pas le token d'agent que
        "flowlio init" affiche. Il est dans ~/.config/flowlio/credentials.json :
        relancez sans FLOWLIO_TOKEN dans l'environnement`, err)
}

// possiblePairs rend n(n−1)/2, le nombre de paires d'une team de n projets. Le chiffre sert à
// dire « 2 sur 3 » plutôt que « 2 », ce qui est la seule façon de voir d'un coup d'œil qu'il
// reste quelque chose à ouvrir.
func possiblePairs(n int) int {
	if n < 2 {
		return 0
	}
	return n * (n - 1) / 2
}

// joinKeys liste les clés de projets, séparées par des virgules.
func joinKeys(projects []service.Project) string {
	keys := make([]string, 0, len(projects))
	for _, p := range projects {
		keys = append(keys, p.Key)
	}
	return strings.Join(keys, ", ")
}

// teamOption rend l'option --team à recopier telle quelle dans la commande suggérée, vide quand
// la team n'a pas été précisée — c'est-à-dire quand le token en porte déjà une.
func teamOption(team string) string {
	if team == "" {
		return ""
	}
	return " --team " + team
}
