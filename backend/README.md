# LLM Gateway (Go)

Go implementation of the LLM Gateway — same HTTP surface, same PostgreSQL
schema, drop-in replacement for the existing admin UI and LLM SDKs.

## Why

Go gives us a single statically-linked binary, native concurrency, and an
order-of-magnitude reduction in memory + cold-start.

## Tech stack

| Layer       | Choice                                                 |
|-------------|--------------------------------------------------------|
| Web         | [Gin](https://github.com/gin-gonic/gin)                |
| DB          | [pgx v5](https://github.com/jackc/pgx) (pgxpool)       |
| JWT         | [golang-jwt/jwt v5](https://github.com/golang-jwt/jwt) |
| Password    | pbkdf2:sha256:600000 (Werkzeug-compatible)             |
| Token est.  | [tiktoken-go](https://github.com/pkoukk/tiktoken-go)   |
| Logging     | `log/slog` (stdlib)                                    |
| Config      | env vars + .env (godotenv)                             |

## Layout

```
backend/
├── cmd/
│   ├── gateway/         main HTTP server
│   └── pwhashcheck/     cross-language hash round-trip helper
├── internal/
│   ├── auth/            password + JWT + API key helpers
│   ├── config/          env loading
│   ├── db/              pgx pool + schema + CRUD
│   ├── handlers/        gin handlers (chat, anthropic, admin, auth, test)
│   ├── middleware/      auth + CORS
│   ├── models/          domain types
│   ├── proxy/           OpenAI / Anthropic forwarding + SSE
│   └── token/           tiktoken + usage normalization
├── Dockerfile
├── go.mod
└── README.md
```

## Quick start

```bash
# 1. Start PostgreSQL
cd ..
docker-compose -f docker-compose.db.yml up -d

# 2. Start the Go server
cd backend
go run ./cmd/gateway
```

### Hot reload (local dev)

Install [Air](https://github.com/air-verse/air) once, then run it from
`backend/`. It watches `cmd/` and `internal/`, rebuilds on save, and
restarts the binary — kill with Ctrl-C.

```bash
go install github.com/air-verse/air@latest
export PATH=$PATH:$(go env GOPATH)/bin
cd backend
air
```

Config lives in [`.air.toml`](.air.toml). `tests/` and
`tmp/` are excluded so SQL/test edits don't trigger a rebuild.

The server reads its config from env vars (or a local `.env`). The
relevant keys are:

| Var               | Default              |
|-------------------|----------------------|
| `PORT`            | `5002`               |
| `DB_HOST`         | `localhost`          |
| `DB_PORT`         | `5432`               |
| `DB_NAME`         | `llm_gateway`        |
| `DB_USER`         | `postgres`           |
| `DB_PASSWORD`     | `password`           |
| `DB_TIMEZONE`     | `Asia/Shanghai`      |
| `JWT_SECRET_KEY`  | `dev-secret-key-change-in-production` |
| `LOG_LEVEL`       | `info`               |

The server creates the default admin user (`admin` / `llm_gateway`) on
first boot if the `users` table is empty.

## Cross-language password compatibility

The Go `auth` package emits and verifies the same
`pbkdf2:sha256:NUM$salt$hash` format that Werkzeug 3.x uses, so a user
hash created by the legacy Python backend can be verified here. The
round-trip is covered by:

```bash
python3 internal/auth/cross_test.py
```

## E2E tests

See [tests/](tests/) (added by the e2e task). The suite boots the server
against a fresh database and exercises every route category.
