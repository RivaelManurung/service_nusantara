# service_nusantara

Rewrite of the Nusantara Oleh-Oleh backend. See [ANALYSIS.md](ANALYSIS.md) for
what was wrong with the previous service and why each decision here differs.

## Status

Complete and building: configuration, platform adapters, the HTTP layer,
middleware, authentication, and the **user/auth module** end to end. The
remaining domains (product, shop, cashier, event, voucher, order, cart,
favorite, banner, customer) still need porting — the pattern to follow is in
[`internal/modules/user/`](internal/modules/user/), and the section
[Porting the remaining modules](#porting-the-remaining-modules) lists the steps.
All 23 GORM models are already ported to `internal/model/`.

## Layout

```
cmd/api/                 entrypoint: load config, dial dependencies, serve
internal/
  config/                every env var, read and validated once
  platform/              adapters: postgres, redis, logging
  httpx/                 response envelope, error taxonomy, decoding, paging
  middleware/            request id, logging, recover, CORS, body limit,
                         rate limit, authenticate, require role
  auth/                  JWT manager, session store, password hashing
  model/                 GORM models (ported)
  modules/<feature>/     dto + repository + service + handler + routes
  server/                dependency wiring and graceful shutdown
```

Modules are grouped **by feature, not by layer**. Changing "how login works"
touches one directory instead of the five the previous service spread it across.

## Requirements

- Go 1.24+
- PostgreSQL 13+ (`gen_random_uuid()` is built in; no `uuid-ossp` needed)
- Redis 6+

## Running

```bash
cp .env.example .env      # then edit: JWT_SECRET, DATABASE_URL, REDIS_ADDR
make run
```

`make help` lists every target. `make test-race` runs the suite with the race
detector.

> **Offline note.** This machine has no route to `proxy.golang.org`, so the
> dependency set was resolved from the local module cache. Prefix commands with
> `GOFLAGS=-mod=mod GOPROXY=off GOWORK=off` until network access is available,
> then run `go mod tidy` once to normalise `go.sum`.

## Demo data

```bash
make seed          # create the schema if needed, then seed. Safe to re-run.
make seed-fresh    # clear the seeded tables first. Destructive.
```

The seeder is **idempotent**: every row's UUID is derived from its business key,
so a second run updates the same rows instead of inserting duplicates. There is
no "have I seeded yet?" flag to keep in sync.

```bash
go run ./cmd/seed -only=catalog,shops   # just the parts you are working on
go run ./cmd/seed -skip=orders
go run ./cmd/seed -scale=10             # +180 generated customers for paging
```

Stages, in dependency order: `roles, users, identities, images, catalog, shops,
addresses, banners, vouchers, points, events, carts, favorites, orders`.

**What it writes.** 4 roles; 9 fixture accounts; sign-in identities across all
four login methods; 8 product categories and 28 real oleh-oleh products with
galleries; 5 outlets with per-outlet pricing, stock and cashiers; customer
addresses; 4 banners; 6 vouchers (one deliberately expired) with claimed
snapshots; loyalty balances backed by a 5-entry ledger each; 3 campaigns
(percentage discount, buy-and-get bundle, one already finished); open carts;
favourites; and 10 orders that between them cover **every** status in the
lifecycle, both fulfilment types and all three payment methods.

Order totals are computed from their lines rather than typed in, so the fixture
cannot drift into a state the application would consider corrupt.

**Accounts.** All use the password `NusantaraDemo123!`, and the command prints
the full list when it finishes.

| Account | Role | Signs in with |
| --- | --- | --- |
| `superadmin@nusantara.test` | superadmin | password |
| `admin@nusantara.test` | admin | password |
| `kasir.malioboro@nusantara.test` | cashier | password |
| `budi@nusantara.test` | customer | password + Google |
| `siti@nusantara.test` | customer | password + Apple |
| `+6281200000003` | customer | phone only — no email, no password |
| `agus@nusantara.test` | customer | Google only — `/auth/login` must reject it |

Addresses use the `.test` TLD, which by RFC 6761 can never resolve, so a seeded
database cannot accidentally email a real person.

**Safety.** The seeder refuses to run against `APP_ENV=production` without
`-allow-production`, and refuses `-truncate` there outright. Elsewhere
`-truncate` still requires an explicit `-yes`.

## API

Envelope field names (`status_code`, `message`, `data`, `error`, `pagination`)
are unchanged from the previous service, so the existing mobile clients keep
working.

| Method | Path | Auth | Purpose |
| --- | --- | --- | --- |
| GET | `/health/live` | – | process is up |
| GET | `/health/ready` | – | postgres + redis reachable |
| POST | `/api/v1/auth/register` | – | email + password sign-up |
| POST | `/api/v1/auth/login` | – | email + password sign-in |
| POST | `/api/v1/auth/google` | – | verify a Google ID token |
| POST | `/api/v1/auth/apple` | – | verify an Apple ID token |
| POST | `/api/v1/auth/phone/request` | – | send a one-time code by SMS |
| POST | `/api/v1/auth/phone/verify` | – | exchange the code for a session |
| POST | `/api/v1/auth/refresh` | – | rotate the refresh token |
| POST | `/api/v1/auth/logout` | Bearer | revoke the current session |
| GET | `/api/v1/auth/profile` | Bearer | the caller's own account |
| GET | `/api/v1/auth/methods` | Bearer | how this account can sign in |
| PATCH | `/api/v1/auth/password` | Bearer | change password, ending every session |

Success:

```json
{ "status_code": 200, "message": "login success", "data": { "access_token": "…" } }
```

Failure — the `error.code` is stable, the `message` is for humans:

```json
{
  "status_code": 422,
  "message": "request validation failed",
  "error": {
    "code": "VALIDATION_ERROR",
    "details": [{ "field": "email", "message": "must be a valid email address" }]
  }
}
```

## Sign-in methods

```
Login
├── Continue with Google     POST /auth/google
├── Continue with Apple      POST /auth/apple
├── Email & Password         POST /auth/register, POST /auth/login
└── Phone / OTP              POST /auth/phone/request, POST /auth/phone/verify
```

All four converge on the same account and the same token pair. A person can
collect several methods over time: `users` holds the account, `user_identities`
holds one row per method (`provider` + `subject`).

### Google and Apple

The app runs the native flow and posts the resulting **ID token**; the backend
re-verifies it against the provider's JWKS and never trusts the client's own
result. Verification pins RS256, the issuer, and the audience — a token minted
for a different app is rejected. Send the `nonce` the app gave the provider and
it is checked too, which blocks replay of a token captured elsewhere.

```jsonc
POST /api/v1/auth/google
{ "id_token": "eyJhbGciOi…", "nonce": "…", "name": "Budi" }   // name: Apple first sign-in only
```

Set `GOOGLE_CLIENT_IDS` / `APPLE_CLIENT_IDS` to the client IDs you accept.
**An empty list disables the provider** rather than accepting every audience.

**Account linking rule.** An unknown provider identity is linked to an existing
account only when the provider reports the email as *verified*. An unverified
address returns 409 instead, because otherwise anyone could sign up elsewhere
with someone's address and take over their account here. Apple private-relay
addresses are verified and stable per app, so they link normally.

### Phone / OTP

```jsonc
POST /api/v1/auth/phone/request   { "phone": "081234567890" }
→ { "expires_in_seconds": 300, "resend_in_seconds": 60 }

POST /api/v1/auth/phone/verify    { "phone": "081234567890", "code": "123456", "name": "Dewi" }
→ { "access_token": "…", "refresh_token": "…" }
```

`081234567890`, `+6281234567890` and `6281234567890` all normalise to one E.164
number, so a person cannot end up with two accounts and two order histories.

Codes come from `crypto/rand`, are stored only as a hash, expire after
`OTP_TTL`, and are burned after `OTP_MAX_ATTEMPTS` wrong guesses.
`OTP_RESEND_COOLDOWN` bounds both SMS spend and using the endpoint to bomb a
third party. The response is identical whether or not the number has an account,
so the endpoint cannot be used to test which numbers are registered.

**No SMS provider ships yet.** `OTP_SENDER=log` writes the code to the log so
the flow is fully testable offline; production refuses to start with it. To add
one, implement `otp.Sender` (two methods) in `internal/platform/otp/` and select
it in `internal/server/providers.go`.

### Email and password

Unchanged, with one addition: an account created through Google or phone has no
password, so `/auth/login` rejects it rather than comparing against an empty
hash. Such a user sets a password through the normal change-password flow once
they have a session.

## Authentication model

- **Access token** — JWT, HS256 pinned, 15 minutes, carries `user_id`, `role`
  and a unique `jti`.
- **Refresh token** — opaque random bytes, 30 days, stored in Redis as a SHA-256
  hash and **single use**: refreshing rotates it and revokes the previous access
  token.
- **Revocation** — by `jti`, with a TTL matching the access token, so the
  blacklist cannot grow without bound. Sessions are per device, and changing a
  password ends **every** session the account holds, not only the calling one.
- **Failure mode** — if Redis cannot answer a revocation check, the request is
  refused with 503 rather than allowed.

## Porting the remaining modules

For each legacy feature, create `internal/modules/<feature>/` with:

1. `dto.go` — request/response structs with `validate:"…"` tags. Delete the
   hand-written `strings.TrimSpace(x) == ""` checks; the tags replace them.
2. `repository.go` — a small interface plus a domain struct. Translate
   `gorm.ErrRecordNotFound` into a package-level `ErrNotFound` here, so the
   service layer never imports GORM.
3. `repository_gorm.go` — the implementation. Always `WithContext(ctx)`.
4. `service.go` — business rules. Return `httpx.*` errors, never raw driver
   errors. Take dependencies as narrow interfaces so they can be faked.
5. `handler.go` — decode with `httpx.DecodeJSON`, delegate, render. Handlers
   return `error`; `httpx.Handler` writes it.
6. `routes.go` — a `Register(mux, prefix, handler, authenticate, rateLimit)`
   function. Mount `rateLimit` *inside* `authenticate` on protected routes, so
   the limiter can key on the authenticated user rather than the shared IP.
7. `service_test.go` — fakes for the ports, table-driven where it fits.

Then add one line to `internal/server/server.go`. Reference implementation:
`internal/modules/user/`.

Two things to fix while porting rather than carry over: the duplicated cashier
stack (finding #20) and the unbounded list queries (#15 — use
`httpx.ParsePagination`).

## Schema note

`users` changed shape for multi-method sign-in: `username`, `email`, `phone` and
`password` are now **nullable**, and `email_verified_at` / `phone_verified_at`
were added. A phone-only customer has no email; a Google user has no password.

This also fixes a latent bug in the previous schema, where `phone` was a
non-null string with a unique index — the second account without a phone number
collided with the first on the empty string.

Seed the role named by `DEFAULT_SIGNUP_ROLE` (default `customer`) before
enabling self-service sign-up; without it those endpoints return 500.

## Not yet carried over

Cloudinary uploads, an SMS provider for OTP delivery, and the RabbitMQ workers
are **not** in this rewrite. Their client libraries (`cloudinary-go`,
`streadway/amqp`, Twilio) are not in the local module cache and could not be
fetched offline. When adding them, define the port in the consuming module and
put the adapter under `internal/platform/`, so the service layer stays testable
without the network. Prefer `rabbitmq/amqp091-go` over the unmaintained
`streadway/amqp` the previous service used, and give the connection a mutex —
the legacy globals were raced (finding #16).

## Before deploying

Rotate every credential that appeared in `nusantara_service/.env`; see
[ANALYSIS.md](ANALYSIS.md) finding #2.
# service_nusantara
