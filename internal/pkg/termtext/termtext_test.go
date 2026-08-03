package termtext

// Ce que ce fichier verrouille : chaque famille de séquence hostile est neutralisée, et l'ordre
// neutraliser → tronquer est respecté.
//
// Ce n'est PAS le contrôle réel du produit. Le contrôle réel est le test qui prouvera qu'aucune
// vue n'oublie d'appeler ce paquet — un filtre parfait qu'une seule ligne de rendu contourne ne
// protège rien. Ces tests garantissent que le filtre fait ce qu'il annonce ; le garde-fou de
// couverture viendra avec le premier renderer.

import (
	"strings"
	"testing"
	"unicode"
)

// famille décrit une charge hostile et ce qui ne doit pas survivre.
type famille struct {
	nom string
	// charge est le texte tel qu'un agent d'un autre repo pourrait l'écrire dans un titre.
	charge string
	// interdit énumère ce qui ne doit PAS ressortir. On y met les CARACTÈRES DE CONTRÔLE, jamais
	// le résidu textuel d'une séquence : privé de son introducteur ESC, « [2J » n'est plus qu'un
	// texte inerte que le terminal affiche sans l'interpréter. Exiger sa disparition demanderait
	// une liste noire — ce que ce paquet refuse d'être — et mangerait un rapport de bug qui parle
	// de séquences d'échappement.
	interdit []string
	// garde énumère ce qui doit ressortir : on encadre le risque, on ne mutile pas le texte.
	garde string
}

var familles = []famille{
	{
		nom:      "CSI — repeindre l'écran, donc mentir sur l'état de la team",
		charge:   "\x1b[2J\x1b[Htout va bien",
		interdit: []string{"\x1b"},
		garde:    "tout va bien",
	},
	{
		nom:      "OSC 52 — écrit le presse-papier système",
		charge:   "panne\x1b]52;c;ZXhmaWx0cmF0aW9u\x07 de login",
		interdit: []string{"\x1b", "\x07"},
		garde:    "panne",
	},
	{
		nom:      "OSC 8 — hyperlien cliquable vers une adresse que personne n'a écrite",
		charge:   "\x1b]8;;http://evil.example\x1b\\cliquez ici\x1b]8;;\x1b\\",
		interdit: []string{"\x1b", "\x1b\\"},
		garde:    "cliquez ici",
	},
	{
		nom: "DSR — fait ÉCRIRE le terminal sur son propre stdin, que le TUI relit comme des touches",
		// La seule famille du lot qui va jusqu'à l'exécution.
		charge:   "état ?\x1b[6n",
		interdit: []string{"\x1b"},
		garde:    "état ?",
	},
	{
		nom: "C0 nu — AUCUN ESC, donc invisible à tout filtre qui ne cherche que 0x1b",
		// Le retour chariot réécrit la ligne depuis son début : l'humain ne lit jamais « tout va
		// bien », seulement « ALERTE ».
		charge:   "tout va bien\rALERTE",
		interdit: []string{"\r"},
		garde:    "ALERTE",
	},
	{
		nom:      "C1 huit bits — introducteur CSI mono-octet",
		charge:   "avant\u009b2Kaprès",
		interdit: []string{"\u009b"},
		garde:    "après",
	},
	{
		nom:      "retour à la ligne dans un titre — insère une fausse rangée dans le tableau",
		charge:   "ligne vraie\nligne fabriquée",
		interdit: []string{"\n"},
		garde:    "ligne fabriquée",
	},
	{
		nom: "contrôles bidi — Trojan Source : l'affiché ne dit pas ce que le titre contient",
		// U+202E RIGHT-TO-LEFT OVERRIDE.
		charge:   "corrige \u202eeitros al ne stnedifnoc sel",
		interdit: []string{"\u202e"},
		garde:    "corrige",
	},
	{
		nom:      "largeur nulle — sépare visuellement rien, casse une comparaison de clés",
		charge:   "CO\u200bRE",
		interdit: []string{"\u200b"},
		garde:    "CORE",
	},
	{
		nom:      "DEL",
		charge:   "avant\x7faprès",
		interdit: []string{"\x7f"},
		garde:    "aprè",
	},
}

// Chaque famille est neutralisée, et ce qui restait de légitime survit.
//
// MUTATIONS : ne filtrer que 0x1b → la ligne « C0 nu » rouge. Ignorer les C1 → la ligne C1 rouge.
// Garder les Cf → la ligne bidi rouge.
func TestLineNeutralisesHostileText(t *testing.T) {
	for _, f := range familles {
		t.Run(f.nom, func(t *testing.T) {
			got := Line(f.charge, 0)

			for _, mauvais := range f.interdit {
				if strings.Contains(got, mauvais) {
					t.Errorf("la séquence %q survit dans %q", mauvais, got)
				}
			}
			if f.garde != "" && !strings.Contains(got, f.garde) {
				t.Errorf("le texte légitime %q a disparu de %q — on encadre le risque, "+
					"on ne mutile pas le contenu", f.garde, got)
			}
			// Contrôle générique : plus AUCUN rune non graphique, quelle que soit la famille.
			for _, r := range got {
				if !unicode.IsGraphic(r) {
					t.Errorf("rune non graphique U+%04X survit dans %q", r, got)
				}
			}
		})
	}
}

// Block conserve les retours à la ligne, convertit les tabulations, et supprime tout le reste.
func TestBlockKeepsStructureAndNothingElse(t *testing.T) {
	got := Block("ligne 1\n\tindentée\rEFFACE\x1b[2Jsuite\n")

	if strings.Count(got, "\n") != 2 {
		t.Errorf("%d retours à la ligne, attendu 2 : un corps est structuré par ses lignes\n%q",
			strings.Count(got, "\n"), got)
	}
	if !strings.Contains(got, "  indentée") {
		t.Errorf("la tabulation n'est pas devenue deux espaces: %q", got)
	}
	if strings.ContainsAny(got, "\r\x1b\t") {
		t.Errorf("un contrôle survit: %q", got)
	}
	if !strings.Contains(got, "EFFACE") || !strings.Contains(got, "suite") {
		t.Errorf("du contenu légitime a disparu: %q", got)
	}
}

// LA LARGEUR EST MESURÉE EN CELLULES D'AFFICHAGE, PAS EN RUNES.
//
// Compter des runes décale toute ligne contenant du CJK, un emoji ou un accent décomposé — et un
// tableau dont les colonnes bougent est un tableau qu'on cesse de lire.
//
// MUTATION : rendre 1 pour tous les runes dans runeCells → les cas CJK et emoji rouges.
func TestCellsMeasuresDisplayWidth(t *testing.T) {
	cas := []struct {
		nom   string
		texte string
		want  int
	}{
		{"ascii", "CORE", 4},
		{"accent précomposé", "é", 1},
		{"accent décomposé — la combinante n'occupe aucune colonne", "é", 1},
		{"CJK — deux colonnes par idéogramme", "日本語", 6},
		{"pleine chasse", "ＣＯＲＥ", 8},
		{"mixte", "bug 日本", 8},
		{"vide", "", 0},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			if got := Cells(c.texte); got != c.want {
				t.Errorf("Cells(%q) = %d, attendu %d", c.texte, got, c.want)
			}
		})
	}
}

// L'ORDRE EST NEUTRALISER PUIS TRONQUER, et ce test dit exactement ce qu'il prouve.
//
// IL NE PROUVE PAS UNE PROPRIÉTÉ DE SÛRETÉ. La liste blanche s'applique dans les deux ordres,
// donc aucun rune actif ne survit de toute façon — la première rédaction de ce fichier affirmait
// le contraire, et la mutation l'a démentie.
//
// Ce qu'il prouve est la FIDÉLITÉ DU BUDGET : sous l'ordre inverse, des caractères destinés à
// disparaître consomment des cellules. Trente caractères de largeur NULLE en tête d'un titre
// suffisent à manger la colonne entière et à repousser le texte réel hors du champ. L'écran est
// alors vide, et rien n'a échoué.
//
// MUTATION : tronquer avant de filtrer dans Line → ce test rouge, la sortie ne contient plus que
// l'ellipse.
func TestNeutralisationHappensBeforeTruncation(t *testing.T) {
	// U+200B ESPACE SANS CHASSE : non graphique, donc supprimé — mais compté tant qu'il est là.
	charge := strings.Repeat("\u200b", 30) + "titre lisible"

	got := Line(charge, 20)

	if !strings.Contains(got, "titre lisible") {
		t.Errorf("got = %q — trente caractères invisibles ont consommé la colonne : la troncature "+
			"a eu lieu AVANT le filtre", got)
	}
}

// La borne est en CELLULES, et l'ellipse est comprise dans le budget — pas ajoutée après.
func TestLineTruncatesToCellBudget(t *testing.T) {
	cas := []struct {
		nom   string
		texte string
		cells int
		want  string
	}{
		{"tient tel quel", "CORE", 10, "CORE"},
		{"exactement à la borne", "CORE", 4, "CORE"},
		{"coupé", "titre beaucoup trop long", 10, "titre bea…"},
		{"borne à 1", "titre", 1, "…"},
		{"borne nulle = pas de borne", "titre beaucoup trop long", 0, "titre beaucoup trop long"},
		// Un rune de deux cellules qui ne tient pas dans la place restante est écarté ENTIER :
		// un rune n'a pas de moitié.
		{"CJK coupé sur une frontière impaire", "日本語", 4, "日…"},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			got := Line(c.texte, c.cells)
			if got != c.want {
				t.Errorf("Line(%q, %d) = %q, attendu %q", c.texte, c.cells, got, c.want)
			}
			if c.cells > 0 && Cells(got) > c.cells {
				t.Errorf("la sortie fait %d cellules, au-dessus de la borne %d : l'ellipse a été "+
					"ajoutée au budget au lieu d'y être comprise", Cells(got), c.cells)
			}
		})
	}
}

// Une troncature doit se VOIR. Sans ellipse, un titre coupé se lit comme un titre complet, et
// l'humain croit avoir tout lu — ce qui est pire que de ne pas afficher la ligne.
func TestTruncationIsVisible(t *testing.T) {
	got := Line("un titre qui dépasse largement la colonne", 12)

	if !strings.HasSuffix(got, string(ellipsis)) {
		t.Errorf("aucune ellipse sur %q : la troncature est invisible", got)
	}
}

// Contre-épreuve de tout le fichier : un texte parfaitement normal ressort INTACT.
//
// Sans ce test, un filtre qui supprimerait tout passerait pour correct sur les dix familles
// hostiles.
func TestLegitimateTextIsUntouched(t *testing.T) {
	for _, s := range []string{
		"Le endpoint /login renvoie 500 depuis le déploiement",
		"clé étrangère composite (project_id, team_id)",
		"café — naïve — cœur — 42 %",
		"日本語のタイトル",
		"emoji 🚀 dans un titre",
		"guillemets \"doubles\" et 'simples'",
	} {
		if got := Line(s, 0); got != s {
			t.Errorf("Line(%q) = %q — un texte légitime a été modifié", s, got)
		}
	}
}
