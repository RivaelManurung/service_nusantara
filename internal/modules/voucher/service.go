package voucher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"service_nusantara/internal/httpx"
)

// A percentage discount outside this range is either pointless or free money.
const (
	minDiscountPercent = 1
	maxDiscountPercent = 100
)

// Service holds the business rules.
type Service struct {
	repo Repository
	log  *slog.Logger
}

func NewService(repo Repository, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// List returns one page of vouchers.
func (s *Service) List(ctx context.Context, query ListQuery) ([]Voucher, int64, error) {
	items, total, err := s.repo.List(ctx, query)
	if err != nil {
		return nil, 0, httpx.Internal("failed to load vouchers").WithCause(err)
	}
	return items, total, nil
}

// Get returns a single voucher.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Voucher, error) {
	item, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return Voucher{}, s.translate(err, "failed to load voucher")
	}
	return item, nil
}

// Create adds a voucher.
func (s *Service) Create(ctx context.Context, input Input) (Voucher, error) {
	input, err := normalize(input)
	if err != nil {
		return Voucher{}, err
	}

	taken, err := s.repo.ExistsByCode(ctx, input.Code, uuid.Nil)
	if err != nil {
		return Voucher{}, httpx.Internal("failed to verify code availability").WithCause(err)
	}
	if taken {
		// Checked rather than left to the unique index, so the client gets an
		// explanation instead of a driver message.
		return Voucher{}, httpx.Conflict("a voucher with this code already exists")
	}

	created, err := s.repo.Create(ctx, Voucher{
		ID:              uuid.New(),
		Code:            input.Code,
		DiscountType:    input.DiscountType,
		DiscountAmount:  input.DiscountAmount,
		DiscountPercent: input.DiscountPercent,
		MinimumSpend:    input.MinimumSpend,
		PointCost:       input.PointCost,
		StartDate:       input.StartDate,
		EndDate:         input.EndDate,
		Quota:           input.Quota,
		Description:     input.Description,
		Status:          normalizeStatus(input.Status),
	}, input.CreatedBy)
	if err != nil {
		return Voucher{}, httpx.Internal("failed to create voucher").WithCause(err)
	}
	return created, nil
}

// Update edits a voucher. The status is left alone, since /edit-status owns it.
func (s *Service) Update(ctx context.Context, id uuid.UUID, input Input) (Voucher, error) {
	if _, err := s.repo.FindByID(ctx, id); err != nil {
		return Voucher{}, s.translate(err, "failed to load voucher")
	}

	input, err := normalize(input)
	if err != nil {
		return Voucher{}, err
	}

	taken, err := s.repo.ExistsByCode(ctx, input.Code, id)
	if err != nil {
		return Voucher{}, httpx.Internal("failed to verify code availability").WithCause(err)
	}
	if taken {
		return Voucher{}, httpx.Conflict("another voucher already uses this code")
	}

	updated, err := s.repo.Update(ctx, id, Voucher{
		Code:            input.Code,
		DiscountType:    input.DiscountType,
		DiscountAmount:  input.DiscountAmount,
		DiscountPercent: input.DiscountPercent,
		MinimumSpend:    input.MinimumSpend,
		PointCost:       input.PointCost,
		StartDate:       input.StartDate,
		EndDate:         input.EndDate,
		Quota:           input.Quota,
		Description:     input.Description,
	})
	if err != nil {
		return Voucher{}, s.translate(err, "failed to update voucher")
	}
	return updated, nil
}

// SetStatus publishes or hides a voucher.
func (s *Service) SetStatus(ctx context.Context, id uuid.UUID, status int) error {
	if err := s.repo.UpdateStatus(ctx, id, normalizeStatus(status)); err != nil {
		return s.translate(err, "failed to update status")
	}
	return nil
}

// Delete removes a voucher, refusing once anyone holds a copy of it.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	claimed, err := s.repo.Claimed(ctx, id)
	if err != nil {
		return httpx.Internal("failed to check whether the voucher was claimed").WithCause(err)
	}
	if claimed {
		// Deleting it would strip the code from wallets that already hold it,
		// and orphan the snapshot the redemption flow reads.
		return httpx.Conflict("this voucher has already been claimed; deactivate it instead")
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return s.translate(err, "failed to delete voucher")
	}
	return nil
}

// normalize trims the code and enforces the rules the struct tags cannot: the
// discount type decides which of the two amount columns may be set, and exactly
// one of them must be, because a voucher carrying both has no single answer to
// "how much is off".
func normalize(input Input) (Input, error) {
	var fields []httpx.FieldError

	input.Code = strings.TrimSpace(input.Code)
	if input.Code == "" {
		fields = append(fields, httpx.FieldError{Field: "code", Message: "is required"})
	}

	if !input.EndDate.After(input.StartDate) {
		fields = append(fields, httpx.FieldError{Field: "end_date", Message: "must be after start_date"})
	}

	switch input.DiscountType {
	case DiscountAmount:
		if input.DiscountPercent != 0 {
			fields = append(fields, httpx.FieldError{
				Field:   "discount_percent",
				Message: `must be 0 when discount_type is "amount"`,
			})
		}
		if input.DiscountAmount < 1 {
			fields = append(fields, httpx.FieldError{
				Field:   "discount_amount",
				Message: "must be greater than 0",
			})
		}

	case DiscountPercent:
		if input.DiscountAmount != 0 {
			fields = append(fields, httpx.FieldError{
				Field:   "discount_amount",
				Message: `must be 0 when discount_type is "percent"`,
			})
		}
		if input.DiscountPercent < minDiscountPercent || input.DiscountPercent > maxDiscountPercent {
			fields = append(fields, httpx.FieldError{
				Field: "discount_percent",
				Message: fmt.Sprintf("must be between %d and %d",
					minDiscountPercent, maxDiscountPercent),
			})
		}

	default:
		fields = append(fields, httpx.FieldError{
			Field:   "discount_type",
			Message: "must be one of: amount, percent",
		})
	}

	if len(fields) > 0 {
		return Input{}, httpx.Validation("request validation failed").WithDetails(fields)
	}
	return input, nil
}

// translate turns a repository error into the right HTTP status.
func (s *Service) translate(err error, message string) error {
	if errors.Is(err, ErrNotFound) {
		return httpx.NotFound("voucher not found")
	}
	return httpx.Internal(message).WithCause(err)
}

// normalizeStatus collapses anything that is not "active" to inactive, so an
// out-of-range integer cannot reach the database.
func normalizeStatus(status int) int {
	if status == StatusActive {
		return StatusActive
	}
	return StatusInactive
}
