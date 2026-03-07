# WUU2

Small application written in Go that uses APIs to see what you've been up to across TV, music, and games, returning it as a nicely accessible JSON.

- Trakt: a single last watched movie or TV show.
- World of Warcraft: character location and coordinates, as well as avatar

Planned Additions:
- Apple Music: last song/album/artist listened to.
- Steam: current game being played.
- Spotify: last song/album/artist listened to.
- Retroachievements: score and recent achievements.
- Playstation Trophies: Current game being played, trophy count, recent trophies.
- XBOX: Current game being played, achievements, gamerscore.

Planned Additional Features 
- WoW: Return last played character on the account as opposed to a predefined character.
- WoW: Return additional information like gold amount, achievements, pvp bracket, and mythic+ score

- All this data is returned in an easy to parse JSON object.

## Run
```bash
cd app
go run .
```

## Docker
```bash
docker build -t wuu2:latest ./app
docker run --rm -p 8080:8080 wuu2:latest
```

## Caveats
### World of Warcraft
Although the intention was to use character coordinates to determine online status, the protected character API only updates when a player logs out. Keys supporting online and last online status are still included and updated, however they are in no way reliable. 

## Environment Variables

### Required

- `UPDATE_INTERVAL_MINUTES` (as a Go duration, example: `30m`)
- `TRAKT_ENABLED` (`true`/`false`)
- `BATTLENET_ENABLED` (`true`/`false`)

### HTTP server

- `PORT`
- `ADDRESS` (optional override, example: `:8080` or `localhost:8080`)
- `CORS_ALLOW_ORIGIN` (optional, default `*`; comma-separated allowlist also supported)

### Optional

- `AUTH_SECURITY_CODE` (optional gate code required before `/auth/` redirects)

### Trakt (required when `TRAKT_ENABLED=true`)

- `TRAKT_ID`

### Battle.net (required when `BATTLENET_ENABLED=true`)

- `BATTLENET_REQUEST_URI`
- `BATTLENET_CLIENT_ID`
- `BATTLENET_CLIENT_SECRET`
- `BATTLENET_REALM` (realm ID, example: `1306`)
- `BATTLENET_CHARACTER_ID` (character ID)
- `BATTLENET_REGION` (example: `eu`, `us`)
- `BATTLENET_REDIRECT_URI`
- `BATTLENET_SCOPE`

### Battle.net optional

- `BATTLENET_CHARACTER` (fallback display name)
- `BATTLENET_LOCALE`

## Example `.env`
```env
ADDRESS="localhost:8080"
UPDATE_INTERVAL_MINUTES=30m
CORS_ALLOW_ORIGIN="*"

TRAKT_ENABLED=true
TRAKT_ID="your-trakt-client-id"

BATTLENET_ENABLED=true
BATTLENET_REQUEST_URI="https://eu.api.blizzard.com"
BATTLENET_CLIENT_ID="your-client-id"
BATTLENET_CLIENT_SECRET="your-client-secret"
BATTLENET_REGION="eu"
BATTLENET_REALM="1306"
BATTLENET_CHARACTER_ID="189686081"
BATTLENET_CHARACTER="Wilderyns"
BATTLENET_REDIRECT_URI="http://localhost:8080/auth/battlenet/callback"
BATTLENET_SCOPE="wow.profile"

# Optional hardening for starting OAuth:
AUTH_SECURITY_CODE="set-a-shared-code"
```

## Battle.net Setup

1. In the Battle.net Developer Portal, configure your OAuth client redirect URL:
   - Must exactly match `BATTLENET_REDIRECT_URI`.
2. Start WUU2:
   - `cd app && go run .`
3. Begin OAuth:
   - Open `http://localhost:8080/auth/battlenet/start`
   - If `AUTH_SECURITY_CODE` is set, enter it first (or pass `?code=...`).
4. After login/consent, callback stores token in memory and WoW updates run on the next update tick.

## Endpoints

- `GET /` -> JSON snapshot (`Wuu2`)
- `GET /auth/battlenet/start` -> starts OAuth redirect
- `GET /auth/battlenet/callback` -> OAuth callback handler
