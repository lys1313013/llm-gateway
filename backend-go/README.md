# LLM Gateway (Go)

Go rewrite of the [Python LLM Gateway](../backend) — same HTTP surface, same
PostgreSQL schema, drop-in replacement for the existing admin UI and LLM
SDKs.

## Why

The Python/Flask backend was the bottleneck under sustained traffic. Go
gives us a single statically-linked binary, native concurrency, and an
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
backend-go/
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
├── migrations/          canonical SQL (also applied at startup)
├── Dockerfile
├── go.mod
└── README.md
```

## Quick start

```bash
# 1. Start PostgreSQL (the same instance the Python backend uses)
cd ..
docker-compose -f docker-compose.db.yml up -d

# 2. Start the Go server (port 5002 by default — leaves Python on 5001)
cd backend-go
go run ./cmd/gateway
```

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
first boot if the `users` table is empty — same behaviour as the Python
backend.

## Cross-language password compatibility

The Go `auth` package emits and verifies the same
`pbkdf2:sha256:NUM$salt$hash` format that Werkzeug 3.x uses, so a user
created in either backend can log in via the other. The round-trip is
covered by:

```bash
python3 internal/auth/cross_test.py
```

## E2E tests

See [tests/](tests/) (added by the e2e task). The suite boots the server
against a fresh database and exercises every route category.
