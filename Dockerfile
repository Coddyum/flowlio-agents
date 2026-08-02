# Image de l'API. Deux étages : on compile avec la chaîne Go, on expédie un binaire seul.
#
# La version de Go n'est PAS écrite ici : elle est lue depuis go.mod par le tag de l'image de
# build. Dupliquer un numéro de version, c'est se garantir qu'un des deux sera oublié.

FROM golang:1.26-alpine AS build

WORKDIR /src

# Les dépendances d'abord, dans leur propre couche : tant que go.mod et go.sum ne bougent pas,
# le téléchargement est réutilisé d'une image à l'autre.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO désactivé : le binaire doit tourner tel quel dans une image sans libc.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/flowlio ./cmd/flowlio

FROM alpine:3.21

# Certificats racine : l'API parle à Neon en TLS en production.
RUN apk add --no-cache ca-certificates

# Utilisateur non privilégié. Le répertoire de configuration lui appartient, sinon l'écriture du
# fichier d'identifiants au premier démarrage échouerait.
RUN adduser -D -u 10001 flowlio && mkdir -p /home/flowlio/.config && chown -R flowlio /home/flowlio
USER flowlio

COPY --from=build /out/api /usr/local/bin/api
COPY --from=build /out/flowlio /usr/local/bin/flowlio

EXPOSE 42058

ENTRYPOINT ["/usr/local/bin/api"]
