// Package termtext est l'ÉVIER UNIQUE par lequel passe tout texte qui atteint un terminal humain.
//
// Le pendant terminal de ce que fait `mcp_untrusted.go` pour le contexte d'un agent : le contenu
// écrit par un tiers est une donnée, jamais une consigne — y compris pour un émulateur de
// terminal, qui obéit à ce qu'on lui écrit sans jamais se demander qui l'a écrit.
//
// UN TITRE D'ISSUE EST DU TEXTE HOSTILE. Il est écrit par l'agent d'un autre repo, que personne
// n'a relu, et il finit dans le terminal d'un humain qui supervise. Une séquence d'échappement y
// fait ce qu'elle veut : repeindre l'écran (donc mentir sur l'état de la team), écrire le
// presse-papier système via OSC 52, ou faire ÉCRIRE le terminal sur son propre stdin via DSR —
// que le programme relit comme des frappes.
//
// LISTE BLANCHE, JAMAIS LISTE NOIRE. Un rune est conservé si et seulement si `unicode.IsGraphic`.
// Ce prédicat couvre L, M, N, P, S et Zs ; il exclut donc d'un coup C0, C1, DEL, les Cf (contrôles
// bidi de Trojan Source, ZWJ) et les Co. Une liste noire écrite à la main sur cet espace — CSI,
// OSC, DCS, DSR, C1 huit bits, `\r` nu sans ESC — finit toujours par avoir un trou, et le trou ne
// se voit qu'une fois exploité.
//
// ORDRE : NEUTRALISER, PUIS TRONQUER, PUIS STYLER.
//
// CE QUE CET ORDRE ACHÈTE, ET CE QU'IL N'ACHÈTE PAS — vérifié par mutation, parce que la première
// rédaction de ce commentaire se trompait. Il n'achète RIEN sur la sûreté : la liste blanche
// s'applique de toute façon, donc aucun rune actif ne survit, quel que soit l'ordre. Ce qu'il
// achète est la FIDÉLITÉ DU BUDGET D'AFFICHAGE. Tronquer d'abord fait payer des cellules à des
// caractères qui vont être supprimés — et trente caractères de largeur nulle en tête d'un titre
// suffisent alors à consommer la colonne entière et à repousser le texte réel hors du champ. Le
// filtre reste correct ; l'écran, lui, est vide.
//
// Le troisième temps — styler — vient après pour la raison inverse : une séquence de couleur que
// NOUS émettons ne doit pas être comptée dans la largeur, ni relue par le filtre.
//
// CE QUE CE PAQUET NE COUVRE PAS, écrit plutôt que tu :
//
//   - LES HOMOGLYPHES PURS. « СORE » avec un С cyrillique est graphique, de largeur normale, et
//     visuellement identique à CORE. La seule parade serait une liste blanche de scripts Unicode,
//     qu'on refuse d'imposer à des titres d'issues écrits en français.
//
//   - LE RÉSIDU TEXTUEL D'UNE SÉQUENCE. `\x1b[2J` perd son ESC et laisse « [2J » à l'écran. Ce
//     résidu est INERTE — sans son introducteur, le terminal l'affiche comme n'importe quel
//     texte — mais il est visible, et une charge assez longue peut consommer la largeur d'une
//     colonne et repousser le titre réel hors du champ. C'est une atteinte à la LISIBILITÉ, jamais
//     au contrôle du terminal. Le retirer demanderait de reconnaître les formes de séquences,
//     c'est-à-dire une liste noire — ce que ce paquet refuse d'être, et qui mangerait au passage
//     un rapport de bug parlant de séquences d'échappement.
//
// Les deux sont des risques connus, non couverts, et assumés comme tels.
package termtext

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément  | Résumé                                                                | Ligne |
// |----------|-----------------------------------------------------------------------|-------|
// | Line     | Neutralise un champ d'une ligne et le borne en cellules d'affichage     | 73    |
// | Block    | Neutralise un corps multi-lignes, en conservant ses retours à la ligne  | 108   |
// | Cells    | Mesure la largeur d'AFFICHAGE, pas le nombre de runes                   | 132   |
// | runeCells| Largeur d'affichage d'un seul rune                                      | 148   |
// | truncate | Coupe à n cellules, en posant une ellipse quand il reste du texte       | 168   |
//
// Fin du sommaire.
// =====================================================================

import (
	"strings"
	"unicode"

	"golang.org/x/text/width"
)

// ellipsis marque une troncature. Un caractère, une cellule.
const ellipsis = '…'

// Line neutralise un champ destiné à tenir sur UNE ligne — titre, clé, nom d'auteur — et le borne
// à cells cellules d'affichage.
//
// Les retours à la ligne sont supprimés comme tout le reste : un `\n` dans un titre insère une
// fausse rangée dans un tableau, ce qui décale tout ce qui suit et fabrique une ligne dont
// personne n'a écrit le contenu.
//
// cells <= 0 signifie « pas de borne » : le texte est neutralisé, pas tronqué. C'est le cas d'un
// champ dont la colonne s'adapte, jamais celui d'un champ qu'on aurait oublié de borner — les
// deux se distinguent à la lecture de l'appelant.
func Line(s string, cells int) string {
	var b strings.Builder
	b.Grow(len(s))

	// Neutraliser D'ABORD : tronquer avant filtrerait une séquence coupée en deux, dont la moitié
	// restante peut rester active.
	for _, r := range s {
		if !unicode.IsGraphic(r) {
			continue
		}
		b.WriteRune(r)
	}

	// Les espaces de bord sont retirés APRÈS le filtre : une séquence supprimée laisse derrière
	// elle des espaces qui ne venaient de rien.
	clean := strings.TrimSpace(b.String())
	if cells <= 0 {
		return clean
	}
	return truncate(clean, cells)
}

// Block neutralise un corps multi-lignes.
//
// Deux exceptions à la liste blanche, et deux seulement :
//
//   - `\n` est CONSERVÉ. Un corps de message est structuré par ses lignes ; les supprimer rendrait
//     illisible ce que ce paquet existe pour rendre lisible.
//   - `\t` devient DEUX ESPACES. Une tabulation déplace le curseur à un taquet dont la position
//     dépend du terminal : elle ne peut pas repeindre l'écran, mais elle casse tout alignement
//     calculé en cellules. La convertir garde l'intention (l'indentation) sans l'effet.
//
// Le `\r` n'est PAS conservé, et c'est le cas qui compte : « tout va bien\rALERTE » ne contient
// aucun ESC, donc il traverse tout filtre qui ne cherche que `0x1b`, et il réécrit la ligne
// depuis son début.
func Block(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	for _, r := range s {
		switch {
		case r == '\n':
			b.WriteRune(r)
		case r == '\t':
			b.WriteString("  ")
		case unicode.IsGraphic(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Cells mesure la largeur d'AFFICHAGE d'une chaîne, en colonnes de terminal.
//
// Compter des runes donnerait un alignement faux dès qu'un titre contient du CJK ou un emoji —
// deux colonnes chacun — ou une marque combinante, qui n'en occupe aucune. Un tableau dont les
// colonnes se décalent est un tableau qu'on cesse de lire.
//
// La chaîne est supposée déjà neutralisée : Cells ne filtre pas, il mesure.
func Cells(s string) int {
	total := 0
	for _, r := range s {
		total += runeCells(r)
	}
	return total
}

// runeCells rend la largeur d'affichage d'un rune : 0, 1 ou 2.
//
// Les marques combinantes (Mn, Me) se posent SUR le caractère précédent et n'occupent aucune
// colonne — les compter décalerait toute ligne contenant un accent décomposé.
//
// `width.LookupRune` distingue les formes East Asian Wide et Fullwidth, qui valent deux colonnes.
// Ambiguous vaut 1 : sa largeur dépend de la locale du terminal, et supposer 2 casserait
// l'alignement du cas courant pour protéger un cas rare.
func runeCells(r rune) int {
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) {
		return 0
	}
	switch width.LookupRune(r).Kind() {
	case width.EastAsianWide, width.EastAsianFullwidth:
		return 2
	default:
		return 1
	}
}

// truncate coupe une chaîne DÉJÀ NEUTRALISÉE à n cellules d'affichage.
//
// Quand il reste du texte, la dernière cellule porte une ellipse : sans elle, un titre coupé se
// lit comme un titre complet, et l'humain croit avoir tout lu. L'ellipse fait partie du budget,
// elle ne s'y ajoute pas.
//
// Un rune de deux cellules qui ne tient pas dans la place restante est écarté entièrement : en
// couper la moitié n'a aucun sens, un rune n'a pas de moitié.
func truncate(s string, n int) string {
	if Cells(s) <= n {
		return s
	}
	if n <= 1 {
		return string(ellipsis)
	}

	var b strings.Builder
	used := 0
	for _, r := range s {
		w := runeCells(r)
		if used+w > n-1 {
			break
		}
		b.WriteRune(r)
		used += w
	}
	b.WriteRune(ellipsis)
	return b.String()
}
