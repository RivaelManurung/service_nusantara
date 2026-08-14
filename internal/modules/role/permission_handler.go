package role

import (
	"net/http"

	"service_nusantara/internal/httpx"
)

// PermissionHandler adapts HTTP to PermissionService.
type PermissionHandler struct {
	service *PermissionService
}

func NewPermissionHandler(service *PermissionService) *PermissionHandler {
	return &PermissionHandler{service: service}
}

// permissionBody is the JSON payload for replacing a role's permissions.
//
// A nil slice and an empty slice mean the same thing here -- revoke everything
// -- which is why there is no `required` on the field: refusing an empty list
// would make "this role can do nothing" an unreachable state.
type permissionBody struct {
	Codes []string `json:"permission_codes"`
}

// Catalog handles GET /permission.
func (h *PermissionHandler) Catalog(w http.ResponseWriter, r *http.Request) error {
	items, err := h.service.Catalog(r.Context())
	if err != nil {
		return err
	}

	httpx.OK(w, r, "permissions retrieved", items)
	return nil
}

// ForRole handles GET /role/{id}/permission.
func (h *PermissionHandler) ForRole(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	item, err := h.service.ForRole(r.Context(), id)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "role permissions retrieved", item)
	return nil
}

// Replace handles PUT /role/{id}/permission/edit.
func (h *PermissionHandler) Replace(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	var payload permissionBody
	if err := httpx.DecodeJSON(r, &payload); err != nil {
		return err
	}

	updated, err := h.service.Replace(r.Context(), id, payload.Codes)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "role permissions updated", updated)
	return nil
}
