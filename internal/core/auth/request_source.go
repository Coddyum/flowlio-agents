package auth

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément               | Résumé                                                        | Ligne |
// |-----------------------|---------------------------------------------------------------|-------|
// | countsAgainstIPBucket | Dit si l'IP source doit être comptée du tout                    | 42    |
// | clientIP              | Extrait l'IP client de r.RemoteAddr, sans faire confiance       | 53    |
// | presentedPrefix       | Extrait le préfixe présenté, vide si le token est malformé      | 63    |
//
// Fin du sommaire.
// =====================================================================
//
// Ce que le limiteur lit dans une requête pour en tirer ses clés de comptage. Séparé de
// rate_limit.go parce que ce sont des décisions sur les ENTRÉES — à qui attribuer une tentative
// — et pas sur le comptage lui-même.

import (
	"net"
	"net/http"

	"github.com/Coddyum/flowlio-ia/internal/pkg/crypto"
)

// countsAgainstIPBucket exclut la boucle locale du seau par IP. CHOIX DE SÉCURITÉ DÉLIBÉRÉ.
//
// En mode local — le mode par défaut et open source — la CLI et le serveur MCP de toutes les
// instances d'agent parlent à l'API via 127.0.0.1. Le seau ip:127.0.0.1 y est donc un quota
// GLOBAL et non un quota par source : un seul agent dont le token est révoqué, en boucle de
// retry, consomme la fenêtre en quelques secondes et fait refuser les tokens VALIDES de toutes
// les autres instances jusqu'à la fin de la fenêtre. Ce n'est pas un cas théorique, c'est le
// fonctionnement normal du produit.
//
// Ce qui est perdu en exemptant la boucle locale est faible : un attaquant capable d'émettre
// depuis 127.0.0.1 a déjà un accès local à la machine, donc au fichier de credentials — la
// limite par IP ne le retenait pas.
//
// Ce qui reste actif en local est le seau par PRÉFIXE, qui vise le token et pas la machine. Il
// protège un token DONNÉ contre l'acharnement ; il ne borne PAS un balayage de préfixes
// distincts depuis la boucle locale, qui reste libre — pas plus que la mémoire des compteurs
// qu'un tel balayage fabrique. Limite connue et acceptée, pas compensée ailleurs.
func countsAgainstIPBucket(ip string) bool {
	return !net.ParseIP(ip).IsLoopback()
}

// clientIP renvoie l'IP source de la connexion, port retiré.
//
// r.RemoteAddr est la SEULE source fiable par défaut : c'est le serveur qui l'écrit, pas le
// client. X-Forwarded-For et consorts sont des en-têtes librement forgés — s'y fier ici
// offrirait à l'attaquant une limite par IP qu'il contourne en changeant une chaîne de
// caractères. Le jour où l'API tourne derrière un proxy de confiance, la liste des proxys
// devient de la configuration explicite ; tant qu'elle n'existe pas, on ne devine pas.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// presentedPrefix renvoie la partie publique du token présenté, ou une chaîne vide si le token
// est malformé. Aucune validation : on veut juste savoir quelle cible est visée.
func presentedPrefix(rawToken string) string {
	prefix, _, err := crypto.ParseToken(rawToken)
	if err != nil {
		return ""
	}
	return prefix
}
