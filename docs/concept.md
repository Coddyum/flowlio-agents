## Flowlio-Ia concepte

### Historique

Flowlio est une application que j'ai conçu lors d'un project d'apprentisage
type linear like, donc un project manager qui étais divisé en (team => project => board) un board contenant columsn + task donc un kanban classique
donc tu pouvais avoir une team backend qui avais plusieur project, une team comptabilité avec ces project ect c'étais vraiment pensé comme une entreprise manager enfaite.
Côté task tu avasi plusieurs information

- card id (identifian reconnaissable pas le uuid de la card ex FRNT-34)
- title
- description (la description étais particulière car on a un view lors de l'open du task qui t'offrais un editeur markdown et donc les task avais des descrtiption extemement riche avec H-\*, tableau, block code, tag user ect)
- assign_to (permettais de d'assigner uniquement au user qui avais access a ce board)
- deadline
  ect ect

Archivage des task ect

Flowlio a récement intégrer un mcp ce qui me permetais a mes code car je travaille sur des project multi repo et pour éviter de devoir remplir des .md d'un repo a l'autre pour partager des question, demande, intégration, changement de contrat d'api ect entre mes session claude code, claude avais un des reglère un workflow très preci sur comment utilisé ce mcp efficacement pour travaille sur mon project ect

mais cela est pas optimal car flowlio a l'origine a été conçu pour des humain pas des ia, et donc même si l'interface est très agréable c'est que du bricolage pour faire utilisé mes ia lors de mes session suivie de task ect

### Concepte + idée

Mon idée serais de crée un project manager pour ia (claude, codex, opencode ect), l'objectif serais un peu pareil crée une team (ton project exemple Omiros) dedans t'as des project (= repo) donc dans mon cas t'aurais le repo (omiros-core (backend)) et omiros-web(frontend).

chaque project serais divisé en 2 partie, une parti project work, et une paris other project question, l'idée serais donc

parti 1 = l'endroit pour le claude d'une session gère les tache a faire, il auto gère ça documentation pour chaque task les priorité ect

parti 2 serais un espace pour que les autre project puisse intéroger ce project exempoe project A (ia agent) ce demande si le backend (project B) a changer le contract d'api de X features car elle ne réponds plus, alors la du coup project A pourrais ouvrir un ticket sur project B dans un espace dédié isolé des task de project B un peu comme un issues github, et donc project A verais les ticket pareil et pourrais simplement répondre après une vérification du code

et inversement, après l'idée serais quand même que les ia ne sois quantoné (isolé) a leur team pour éviter de posé des question / task sur des project qui ne les concerne pas

l'autre point que je voudrais implémenter c'est la mémory d'un project, car on le sait un des défaut de l'ia c'est leur mémoire elle sont toujours obligé de relire masse chose alors évidement on met en place souvent des outils obsidian, mempalace, .claude/memory ect ect mais donc y'a pas vraiment de suivie propre et j'ai aucun idée de comment mettre ça en place mais voila une des features que je veux mettre en place

un autre point que j'aimerais bien faire a voir comment mais lors ce que un des agent a répondu a une issues (ticket) ça serais cool de automatiquement relancer la session claude, codex ect qui a posé la question automatiquement.

l'idée étonnament même si c'est une app pour des ia j'ai pas envie d'intégrer d'ia dans le project car je pense que on peu faire du determinisme sur la plus part des choses,

### Interface utilisateur

Pas de frontend, pas d'application desktop,
je voudrais qu'on crée un outils full cli + mcp
donc pas d'interface visuelle pure type page web ou autre, sauf peut-être pour l'inscription ou payment car oui je sais pas trop comment on pourrais faire ça

mais j'aimerais que l'app tourne sois en local en free open source type n8n, ou alors en hosted par nous même et dans ces cas la abonnement par mois via stripe,

donc usage local = pas de compte direct on crée un faut compte utilisateur pas besoin d'email ou password, mais en hosted par nous faut un compte logique

### pensé

tu vois aujourd'hui il existe plein d'outils incroyable comme superset qui permet de lancer plein de session d'ia en parallel dans des workspace isolé, des outils de mémoir ect mais j'ai rien vu qui permet de gérer efficacement la mémoire + tasking inter repo, project session ia, alors peut-être que ma vision de team => project => board avec task + issues n'est pas la bonne faut encore y réfléchir, j'ai donner ça comme idée car cela reprendre ce que j'avais déjà crée pour flowlio, mais c'est pas forcément la meilleurs des idée

ah oui et point le plus important dans ce project tu dois être absolument irréprochable sur l'usage des secret ect car ce project sera open source du moins pour le self hosting
