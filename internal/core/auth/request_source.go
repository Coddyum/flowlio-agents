package auth

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément               | Résumé                                                        | Ligne |
// |-----------------------|---------------------------------------------------------------|-------|
// | countsAgainstIPBucket | Dit si l'IP source doit être comptée du tout                    | 37    |
// | sourceKey             | Ramène une source à son unité de comptage (/64 en IPv6)         | 52    |
// | clientIP              | Extrait l'IP client de r.RemoteAddr, sans faire confiance       | 73    |
//
// Fin du sommaire.
// =====================================================================
//
// Ce que le limiteur lit dans une requête pour en tirer sa clé de comptage. Séparé de
// rate_limit.go parce que ce sont des décisions sur les ENTRÉES — à qui attribuer une tentative
// — et pas sur le comptage lui-même.

import (
	"net"
	"net/http"
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
// CONSÉQUENCE ASSUMÉE, ÉCRITE SANS DÉTOUR : le seau par IP étant le seul qui reste, le limiteur
// ne freine RIEN depuis la boucle locale. C'est cohérent avec le modèle de menace et non avec un
// oubli — un attaquant capable d'émettre depuis 127.0.0.1 lit déjà le fichier de credentials,
// donc il n'a aucune raison de deviner un token. Ce limiteur défend le mode hosted, où la source
// d'une requête est une information ; en local, c'est le système de fichiers qui protège.
func countsAgainstIPBucket(ip string) bool {
	return !net.ParseIP(ip).IsLoopback()
}

// sourceKey ramène une adresse source à l'unité de comptage : l'adresse telle quelle en IPv4,
// le PRÉFIXE /64 en IPv6.
//
// Compter une adresse IPv6 exacte revient à ne rien compter. Le plus petit bloc que reçoit un
// client résidentiel ou une instance cloud est un /64, soit 2^64 adresses : un attaquant change
// d'adresse à chaque requête sans quitter sa machine, et chaque tentative ouvre un compteur neuf
// — le plafond ne mord jamais, et la famille de clés devient fabricable en masse. Le /64 est la
// plus petite unité qui corresponde à « une source ».
//
// La normalisation intervient APRÈS l'exemption de la boucle locale : ::1 réduit à son /64
// donnerait ::, qui n'est plus reconnu comme loopback.
func sourceKey(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		// Fail-closed : une adresse illisible est comptée telle quelle plutôt qu'ignorée.
		return ip
	}
	if parsed.To4() != nil {
		return ip
	}

	prefix := parsed.Mask(net.CIDRMask(64, 128))
	return prefix.String() + "/64"
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
