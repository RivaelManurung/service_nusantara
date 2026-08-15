package point

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/httpx"
)

// Handler adapts HTTP to the Service.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// Balance handles GET /user/{id}/point.
func (h *Handler) Balance(w http.ResponseWriter, r *http.Request) error {
	userID, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	balance, err := h.service.Balance(r.Context(), userID)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "point balance retrieved", balance)
	return nil
}

// History handles GET /user/{id}/point/history.
func (h *Handler) History(w http.ResponseWriter, r *http.Request) error {
	userID, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	page := httpx.ParsePagination(r)

	rows, total, err := h.service.History(r.Context(), HistoryQuery{
		UserID:    userID,
		Page:      page.CurrentPage,
		PerPage:   page.PerPage,
		Direction: strings.TrimSpace(strings.ToLower(r.URL.Query().Get("direction"))),
	})
	if err != nil {
		return err
	}

	httpx.Paginated(w, r, "point history retrieved", rows, page.WithTotal(total))
	return nil
}

// adjustBody is the POST /user/{id}/point/adjust payload.
type adjustBody struct {
	Points    int64  `json:"points"`
	Direction string `json:"direction"`
	Reason    string `json:"reason"`
}

// Adjust handles POST /user/{id}/point/adjust.
func (h *Handler) Adjust(w http.ResponseWriter, r *http.Request) error {
	identity, ok := auth.IdentityFrom(r.Context())
	if !ok {
		return httpx.Unauthorized("authentication required")
	}
	actorID, err := uuid.Parse(identity.UserID)
	if err != nil {
		return httpx.Unauthorized("your session is not valid")
	}

	userID, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	var body adjustBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}

	balance, err := h.service.Adjust(
		r.Context(), actorID, userID, body.Points, body.Direction, body.Reason,
	)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "points adjusted", balance)
	return nil
}

// ClaimedVouchers handles GET /user/{id}/voucher.
func (h *Handler) ClaimedVouchers(w http.ResponseWriter, r *http.Request) error {
	userID, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	rows, err := h.service.ClaimedVouchers(r.Context(), userID)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "claimed vouchers retrieved", rows)
	return nil
}

// Claimants handles GET /voucher/{id}/claims.
func (h *Handler) Claimants(w http.ResponseWriter, r *http.Request) error {
	voucherID, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	page := httpx.ParsePagination(r)

	rows, total, err := h.service.Claimants(
		r.Context(), voucherID, page.CurrentPage, page.PerPage,
	)
	if err != nil {
		return err
	}

	httpx.Paginated(w, r, "voucher claims retrieved", rows, page.WithTotal(total))
	return nil
}
