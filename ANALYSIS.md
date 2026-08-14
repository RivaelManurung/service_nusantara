# Analysis of `nusantara_service` (legacy)

Findings from reading the 192-file Echo/GORM service, ordered by severity. Each
entry names what the rewrite in this directory does instead.

---

## CRITICAL

### 1. Revoked tokens bypassed authentication entirely

`internal/middlewares/jwt_middleware.go:19-33`

```go
Skipper: func(c echo.Context) bool {
    ...
    if err == nil && val == "blacklisted" {
        return true          // <-- "skip the JWT middleware"
    }
    return false
},
```

In `echo-jwt`, a `Skipper` returning `true` means *do not run this middleware*.
A blacklisted token therefore did not merely stay valid: it skipped JWT
verification completely and reached the handler with no `"user"` in the context
at all. Combined with finding #2, the handler then panicked, but any endpoint
that tolerated a missing identity was reachable unauthenticated.

The lookup was also against the wrong key: the middleware read
`blacklist:<token>` while `LogoutAdmin` wrote `blacklist_superadmin:<token>`
(`internal/domain/usecases/user_usecase.go:212`), so the branch rarely fired in
practice — which is the only reason this was not routinely exploited.

**Now:** `internal/middleware/auth.go` verifies first and rejects revoked tokens
with 401. A revocation-store outage returns 503 (fail closed), never "allow".
Regression tests: `internal/middleware/auth_test.go`.

### 2. Live production secrets committed in the working tree

`nusantara_service/.env` contains real values for `DATABASE_URL`,
`REDIS_PASSWORD`, `TWILIO_AUTH_TOKEN`, `CLOUDINARY_API_SECRET` and `AMQP_URL`.
`.env.example:15-16` additionally carries a real-looking Twilio SID and token in
comments.

`.gitignore` is encoded as UTF-16 (it starts with a BOM and NUL-separated
bytes), so git does not read the `.env` line as a pattern at all — the ignore
rule was silently inert.

**Action required, independent of this rewrite:** rotate every credential in
that file, rewrite `.gitignore` as UTF-8, and purge `.env` from any history it
reached.

---

## HIGH

### 3. Unchecked type assertions panic on attacker-controlled input

`internal/handlers/user_handler.go:99-101` and repeated in every protected
handler:

```go
claims := user.Claims.(jwt.MapClaims)
userID := claims["user_id"].(string)
```

A token without `user_id`, or with a non-string one, panics the goroutine. Echo
recovers, but each request still costs a stack unwind and yields a 500.

**Now:** `auth.Identity` is a typed struct placed in the context by the
middleware; handlers call `auth.IdentityFrom(ctx)` and check the boolean
(`internal/modules/user/handler.go:118`).

### 4. `uuid.MustParse` on a request body field

`internal/domain/usecases/user_usecase.go:52` — `RoleID: uuid.MustParse(req.RoleID)`.
`MustParse` panics by contract. Any client could crash the handler by posting
`{"role_id": "x"}`.

**Now:** `uuid.Parse` with a 400 on failure, plus `validate:"uuid"` on the DTO.
Test: `TestRegisterRejectsAMalformedRoleIDWithoutPanicking`.

### 5. Login told the client whether an email existed

`internal/domain/usecases/user_usecase.go:118` returns `"email incorrect"`, and
line 160 returns `"password incorrect"`. That difference turns the login
endpoint into a user-enumeration oracle.

**Now:** one message for both. Test:
`TestLoginGivesTheSameAnswerForUnknownEmailAndWrongPassword`.

### 6. `Access-Control-Allow-Origin: *` on a token-authenticated API

`main.go:44-47`. Every website on the internet could script requests against the
API from a visitor's browser.

**Now:** `middleware.CORS` reflects only allow-listed origins, and
`config.validate` refuses to start in production with an empty list or `"*"`.

### 7. Internal errors returned verbatim to clients

The pattern `response.Error(c, 500, err.Error(), ...)` appears throughout the
handlers, forwarding driver text such as `pq: password authentication failed for
user "admin"` straight to the caller.

**Now:** `httpx.Error` separates the client message from the logged cause;
non-`*httpx.Error` values become a generic 500. Test:
`TestWriteErrorHidesTheCauseOfInternalFailures`.

### 8. One session key per user

`superadmin_token:<id>` (`user_usecase.go:186`) holds a single token, so signing
in on a second device silently invalidated the first, and `GetProfile` compared
the presented token against that one value.

**Now:** sessions are keyed per session id; each device is revocable on its own
(`internal/auth/session.go`).

---

## MEDIUM

### 9. No graceful shutdown

`main.go:71` ends in `log.Fatal(e.Start(...))`. SIGTERM killed the process
mid-request, which during a checkout could interrupt an order between writes.
**Now:** `signal.NotifyContext` plus `http.Server.Shutdown` with a grace period.

### 10. No server timeouts

`echo.New()` leaves `ReadTimeout`, `WriteTimeout` and `IdleTimeout` at zero, so a
slow-loris client could hold connections open indefinitely.
**Now:** all four are configured (`internal/server/server.go`).

### 11. The access logger logged nothing

`internal/middlewares/jwt_middleware.go:41-45` — `LoggerMiddleware` calls
`next(c)` and returns. It was registered in `main.go:41` as if it worked.
**Now:** structured `slog` access logs with a request id.

### 12. Migration errors discarded

`main.go:36` calls `configs.RunMigrations(db)` and ignores the returned error, so
the service came up against an out-of-date schema and failed at the first query.
**Now:** `cmd/api/main.go` returns the error, and `AutoMigrate` is refused in
production.

### 13. Routes logged before they were registered

`main.go:49-51` iterates `e.Routes()` before `routes.Routes(...)` runs, so the
loop always printed nothing.

### 14. Rate limiting only on login

There was no global limiter; every other endpoint was unthrottled.
**Now:** a Redis fixed-window limiter over all routes, keyed by user when
authenticated and by IP otherwise, plus the login-specific lock.

### 15. Unbounded list endpoints

`internal/utils/pagination.go` did not clamp `per_page`, so one request could ask
for an entire table.
**Now:** `httpx.ParsePagination` clamps to 100. Tests in `pagination_test.go`.

### 16. Data race on the RabbitMQ globals

`configs/rabbitmq.go` mutates the package-level `RabbitConn` / `RabbitChannel`
from `GetRabbitChannel` with no mutex, while workers call it concurrently.

### 17. Prepared statements disabled

`configs/db.go:19-23` sets `PreferSimpleProtocol: true` and `PrepareStmt: false`,
giving up statement caching for no stated reason.
**Now:** both defaults restored, plus pool limits (the legacy pool was unbounded).

### 18. `log.Fatal` inside library packages

`InitDB`, `InitRedis` (a `panic`) and `InitRabbitMQ` terminate the process from
deep inside a package, so nothing can be unit tested and no deferred cleanup runs.
**Now:** every constructor returns an error; only `main` exits.

---

## Structural

### 19. Inverted layer naming

Interfaces named `UserService` lived in `internal/data/services/`, while their
implementations lived in `internal/domain/usecases/` — and the implementing
struct was called `userService` inside a package called `usecases`. A reader
following "service" lands in the wrong package every time.

### 20. Two parallel cashier stacks

`internal/handlers/cashier_handler.go` and
`internal/handlers/CashierHandler/cashier_handler.go`; likewise
`domain/usecases/cashier_usecase.go` vs `domain/usecases/CashierUsecase/`,
`data/repositories/cashier_repository_impl.go` vs
`data/repositories/CashierRepoImpl/`. Both are wired in `routes/route.go:31-32`
onto the same `/cashier` group. Mixed `PascalCase` and `snake_case` directories
compound the confusion.

**Now:** one directory per feature under `internal/modules/`, all lowercase.

### 21. No tests

Zero `_test.go` files across 192 files.

### 22. Build artefacts committed

`nusantara_service`, `main`, and `tmp/main.exe` are compiled binaries sitting in
the source tree.
