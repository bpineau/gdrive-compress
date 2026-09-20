# gdrive-compress

Recompresse en place les images JPEG de ton Google Drive pour libérer de l'espace.
Cible une taille maximale de `600 KiB` (configurable) en perdant le moins possible
en qualité : binary search sur le quality factor JPEG, puis downscale progressif
si nécessaire. EXIF préservé. `modifiedTime` du fichier Drive préservé.

## Setup

### 1. Installer ImageMagick

```bash
brew install imagemagick
magick -version   # vérifie que ça répond
```

### 2. Créer un projet Google Cloud + un client OAuth

Tout est gratuit, pas besoin d'activer la facturation. Tu fais ça **une fois**.

#### 2.1 Créer le projet

1. Va sur https://console.cloud.google.com/
2. Sélecteur de projet en haut à gauche → **"Nouveau projet"** → nomme-le (ex: `gdrive-compress`) → Créer.
3. **Important** : à chaque étape qui suit, vérifie que ton nouveau projet est bien sélectionné en haut de page (le sélecteur garde parfois l'ancien).

#### 2.2 Activer l'API Drive

Va sur https://console.cloud.google.com/apis/library/drive.googleapis.com et clique **"Activer"** (le bouton bleu). Si tu vois "API activée", c'est déjà fait.

#### 2.3 Configurer l'écran de consentement OAuth

L'UI a été refondue récemment, voici la version à jour :

1. Va sur https://console.cloud.google.com/auth/branding
2. Clique **"Get started"** / **"Commencer"**.
3. **App information** :
   - App name : `gdrive-compress` (libre)
   - User support email : ton email
4. **Audience** : choisis **External** / **Externe**.
5. **Contact information** : ton email comme developer contact.
6. **Finish** / **Terminer**.

Ensuite ajoute ton compte Gmail comme **testeur autorisé** - sinon l'auth sera bloquée avec "Access blocked: app has not completed verification" :

1. Va sur https://console.cloud.google.com/auth/audience
2. Section **"Test users"** → **"+ Add users"** → saisis l'adresse Gmail du compte dont tu veux compresser les photos (par ex. `monemail@gmail.com`) → **Save**.
3. La liste prend effet immédiatement, pas besoin d'attendre.

ℹ️ Tu peux laisser l'app en mode **"Testing"** indéfiniment pour un usage perso. Le bouton "Publish app" / "Production" déclenche au contraire une procédure de vérification - ne le presse pas.

#### 2.4 Créer le client OAuth

1. Va sur https://console.cloud.google.com/apis/credentials
2. **"+ Create Credentials"** (en haut) → **"OAuth client ID"**.
3. Application type = **Desktop app**, nom au choix → **Create**.
4. Une popup affiche `Client ID` et `Client secret`. Tu peux fermer.

#### 2.5 Télécharger le credentials.json

Sur https://console.cloud.google.com/apis/credentials, dans la liste **"OAuth 2.0 Client IDs"** :

- Sur la ligne de ton client, à droite tu vois deux icônes (crayon Modifier, poubelle Supprimer).
- **Clique sur le crayon "Modifier"** → la page d'édition s'ouvre.
- En haut à droite de la page d'édition, bouton **"DOWNLOAD JSON"** / **"TÉLÉCHARGER LE JSON"** → le navigateur télécharge `client_secret_XXXXX-YYYYY.apps.googleusercontent.com.json` dans `~/Downloads`.

Place-le dans le projet sous le nom `credentials.json` :

```bash
mv ~/Downloads/client_secret_*.apps.googleusercontent.com.json \
   <dossier-du-depot>/credentials.json
```

> **Si rien ne se télécharge** au clic sur "DOWNLOAD JSON" (bloqueur de popups, etc.), reconstruis le fichier à la main avec les valeurs visibles sur la page d'édition :
>
> ```bash
> cat > credentials.json <<'EOF'
> {
>   "installed": {
>     "client_id": "COLLE_LE_CLIENT_ID",
>     "client_secret": "COLLE_LE_CLIENT_SECRET",
>     "redirect_uris": ["http://127.0.0.1"],
>     "auth_uri": "https://accounts.google.com/o/oauth2/auth",
>     "token_uri": "https://oauth2.googleapis.com/token"
>   }
> }
> EOF
> ```

### 3. Build

```bash
cd <dossier-du-depot>
go build .
```

### 4. Première authentification

```bash
./gdrive-compress --quota
```

- Le navigateur s'ouvre sur l'écran de consentement Google.
- **Choisis le compte Gmail que tu as ajouté comme testeur** (étape 2.3).
- Tu verras un avertissement **"Google n'a pas vérifié cette application"** - c'est normal puisque l'app est la tienne et n'a pas été soumise à validation. Clique **"Avancé"** → **"Accéder à gdrive-compress (non sécurisé)"**.
- Coche les permissions demandées (lecture/écriture Drive) → **Continuer**.
- Onglet "Authentification réussie", tu peux fermer.

Le **refresh token** est sauvegardé localement dans
`~/Library/Application Support/gdrive-compress/token.json` (mode 0600).
Tu n'auras plus à refaire ce flow tant que tu ne supprimes pas ce fichier ou que
tu ne révoques pas l'accès depuis https://myaccount.google.com/permissions.

`./gdrive-compress --quota` affichera ton usage actuel. Si ça marche, tu es prêt.

#### Erreurs courantes au premier auth

| Erreur | Cause | Fix |
|---|---|---|
| `Access blocked: gdrive-compress has not completed verification` | Email non listé comme testeur | Ajoute-le dans [auth/audience](https://console.cloud.google.com/auth/audience) § Test users |
| `Error 400: redirect_uri_mismatch` | Mauvais type de client OAuth | Recrée en **Desktop app**, pas Web app |
| `credentials.json not found` | Fichier mal placé ou mal renommé | `ls credentials.json` dans le dossier projet |
| `oauth2: token expired and refresh token is not set` | Refresh token périmé / révoqué | `rm ~/Library/Application\ Support/gdrive-compress/token.json` puis relance |

## Usage

### Voir l'usage actuel (live, frais à la seconde)

```bash
./gdrive-compress --quota
```

L'UI Drive met **24-48 h** à se mettre à jour ; cette commande appelle l'API
`about.get` qui est temps-réel.

### Dry-run avec inspection visuelle

```bash
./gdrive-compress --limit 5 --dump-dir ./samples
```

- Liste tous les JPEG > 600 KiB de ton Drive (paginé).
- Télécharge et recompresse les 5 premiers **en mémoire**.
- Écrit dans `./samples/` une paire `<id>_orig_<nom>.jpg` + `<id>_new_<nom>.jpg` par image, pour comparer côte à côte.
- **N'écrit rien dans Drive.**
- Log JSONL dans `processed.jsonl` ; les lignes `dry: true` sont ignorées au prochain run, donc une vraie passe `--apply` ne saute pas ces fichiers.

Comparaison rapide sous macOS :

```bash
open ./samples
# Sélectionne une paire dans le Finder, barre d'espace → flèches ←/→
```

### Pour de vrai

```bash
./gdrive-compress --apply
```

Reprend où ça s'est arrêté : `processed.jsonl` mémorise les fichiers déjà remplacés avec succès, ils sont sautés au run suivant. Tu peux interrompre avec Ctrl-C, le fichier en cours termine proprement.

### Flags

| Flag | Défaut | Description |
|---|---|---|
| `--apply` | `false` | Sans ce flag, dry-run (rien n'est modifié dans Drive). |
| `--quota` | `false` | Affiche l'usage live et quitte. |
| `--dump-dir` | `""` | Si défini, écrit `<id>_orig_…` + `<id>_new_…` dans ce dossier pour inspection visuelle. |
| `--max-kib` | 600 | Cible maximale en KiB. |
| `--threshold-kib` | 600 | Ignore les fichiers déjà ≤ cette taille (filtré au listing, pas de download). |
| `--limit` | 0 | Traite au plus N fichiers (0 = pas de limite). |
| `--concurrency` | 3 | Workers parallèles. |
| `--log` | `processed.jsonl` | Fichier de progression (reprise sur interruption). |

## Comment c'est fait

- **Listing** : `files.list` avec `q="mimeType='image/jpeg' and trashed=false"`, paginé (1000/page), filtré côté client sur `Size >= --threshold-kib`.
- **Compression** : appel `magick` en sous-process. Binary search du quality factor sur `[40, 92]`, on garde le plus haut quality dont la taille ≤ `--max-kib`. Si même q=40 dépasse, on downscale le côté le plus long progressivement (2400 → 1920 → 1600 → 1280 → 1024 px) et on relance la binary search à chaque palier. EXIF préservé via les options `-sampling-factor 4:2:0 -interlace Plane -define jpeg:optimize-coding=true` (pas de `-strip`).
- **Remplacement** : `files.update` avec `Media()`. On repasse explicitement le `modifiedTime` original pour ne pas perturber la timeline / le tri Drive.
- **Robustesse** : retries exponentiels sur 429/5xx jusqu'à 5 tentatives. Si la version recompressée est ≥ l'originale (déjà bien compressée), on saute. Log JSONL append-only pour reprise après interruption ou crash.
- **Pas de race** : les workers (par défaut 3) traitent des fichiers distincts, et les écritures dans `processed.jsonl` sont sérialisées par mutex.

## ⚠ Le piège des révisions

Quand tu remplaces le contenu d'un fichier dans Drive, **l'ancienne version
reste stockée comme révision pendant 30 jours** par défaut. Conséquence : après
`--apply`, ton quota total **ne baisse pas immédiatement** parce que les anciennes
versions (lourdes) comptent toujours. Tu peux le vérifier avec `--quota` : la
nouvelle taille du fichier est bien remontée mais l'usage total reste haut.

Pour libérer la place tout de suite il faut supprimer les anciennes révisions
via `revisions.list` + `revisions.delete`. Si tu veux ce comportement, demande
l'ajout d'un flag `--purge-revisions`.

## Ce qui n'est PAS géré (volontairement)

- **PNG** : la plupart des photos viennent du téléphone en JPEG. Si tu as beaucoup de PNG > 600 KiB après ce nettoyage, on ajoutera un mode PNG→JPEG.
- **Google Photos** : ses photos passent par une autre API (Library API), très restreinte depuis mars 2025 (impossible de remplacer en place le contenu uploadé par d'autres apps). Hors scope.
- **HEIC** : rarement présent dans Drive. À ajouter au besoin.
- **Vidéos** : utilise `ffmpeg` séparément (encodage 2-pass cf. exemple `reno-apres`).

## Sécurité

- Scope demandé : `drive` (lecture + écriture sur tout ton Drive). Nécessaire pour remplacer du contenu existant.
- Le token reste **local** dans `~/Library/Application Support/gdrive-compress/token.json` (mode 0600).
- Aucune communication réseau en dehors de Google Drive (pas d'upload ailleurs, pas de télémétrie).
- Pour révoquer : https://myaccount.google.com/permissions → cherche `gdrive-compress` → "Supprimer l'accès".
- Tu peux aussi supprimer le client OAuth dans la Cloud Console après usage.

## Reprise / debug

- Voir ce qui a été fait : `cat processed.jsonl | jq .`
- Compter par statut :
  ```bash
  jq -r 'if .error then "err" elif .skipped then "skip" elif .dry then "dry" else "ok" end' processed.jsonl | sort | uniq -c
  ```
- Repartir de zéro : `rm processed.jsonl` (mais tu re-traiteras tout).
- Re-tenter uniquement les erreurs : c'est déjà le comportement par défaut (les lignes avec `error` sont ignorées au chargement).
