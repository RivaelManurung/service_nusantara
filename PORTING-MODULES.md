# Porting a CRUD module from `nusantara_service`

Reference implementation: **`internal/modules/typeproduct/`**. Copy its shape.

## Files

```
internal/modules/<name>/
├── <name>.go            response struct, Input, ListQuery, Repository port, ErrNotFound
├── repository_gorm.go   the GORM implementation
├── service.go           business rules; returns httpx.* errors
├── handler.go           parse → delegate → render
└── routes.go            Register(mux, prefix, handler, authenticate, rateLimit)
```

## Rules

1. **The legacy six layers collapse to four files.** Do not recreate
   `data/source` + `data/repository` + `domain/repository` + `domain/usecases` +
   `data/services`. No `IRepository`, no `UseCase` structs.
2. **Response field names are fixed by the web client.** Read
   `web_nusantara/src/features/<feature>/types.ts` for the exact DTO before you
   write the Go struct. Getting a key name wrong silently breaks the UI.
3. **URL shapes are fixed too**: `GET /x`, `GET /x/{id}`, `POST /x/create`,
   `PUT /x/{id}/edit`, `PUT /x/{id}/edit-status`, `DELETE /x/{id}/delete`.
   Read the legacy `routes/*_routes.go` to confirm per module.
4. **Multipart, not JSON**, wherever the legacy handler read `c.FormValue`.
   Use `httpx.ParseMultipart` and the `Form` helpers — they collect every field
   error and report them together.
5. **Never trust the body for identity.** Take the acting user from
   `auth.IdentityFrom(r.Context())`, as the reference handler does.
6. **Upload before insert.** A storage failure must not leave a row pointing at
   an image that was never stored.
7. **Errors**: repository returns a package-level `ErrNotFound`; the service
   maps it to `httpx.NotFound`. Never let a driver message reach the client.
8. **Status is an integer** (`0` inactive, `1` active) — clamp anything else.
9. **Pagination** via `httpx.ParsePagination(r)` and `httpx.Paginated(...)`;
   never return an unbounded list.
10. **Search** with `ILIKE` and escape the user's `%` and `_` (see
    `escapeLike`).
11. Guard deletes that would violate a foreign key with an explicit
    `httpx.Conflict` rather than letting Postgres raise a 500.

## Wiring

Do NOT edit `internal/server/`. Return the exact two lines to add to
`registerCatalogModules` in your report; the coordinator applies them.

## Checks

```
gofmt -l .        # must print nothing
go build ./...
go vet ./...
go test ./...
```
