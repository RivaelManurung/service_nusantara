// Package customeraddress manages the delivery addresses a customer keeps on
// their account, plus the "which shops are near me" lookup that checkout and
// the storefront both start from.
//
// It follows the shape set out by internal/modules/typeproduct: one package
// holds the response types, the persistence port, the business rules and the
// HTTP handlers, in that order.
//
// Two invariants drive the whole module:
//
//   - Every read and every write is scoped to the authenticated user. There is
//     no lookup by address id alone anywhere in the persistence port, so a
//     handler cannot accidentally expose one customer's address to another.
//   - A customer has at most one default address. Promoting one demotes the
//     rest inside a single transaction, because checkout picks "the default"
//     with a LIMIT 1 and would otherwise choose arbitrarily between two.
package customeraddress

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when no row matches for this owner -- which covers
// both "no such address" and "not yours". The two are deliberately
// indistinguishable to the caller: telling an attacker that an id exists but
// belongs to someone else is itself a leak.
var ErrNotFound = errors.New("customer address not found")

// ErrTooMany is returned when the owner has hit maxAddressesPerUser.
var ErrTooMany = errors.New("customer address limit reached")

// maxAddressesPerUser bounds both the address list and the insert. The list
// endpoint is not paginated -- a saved-addresses screen shows all of them, and
// paging it would change the client contract -- so the cap is what keeps the
// response bounded, as an unpaginated list otherwise must be.
const maxAddressesPerUser = 50

// Shop status values, mirroring internal/model. Only active shops are ever
// returned by the nearby lookup.
const (
	shopStatusActive = 1
)

// Search radius and result cap for the nearby-shops lookup. The legacy handler
// hard-coded 10km and the repository hard-coded LIMIT 20; both are kept, but
// named, because the pair is what bounds the query's cost.
const (
	DefaultRadiusKM = 10.0
	MaxNearbyShops  = 20
)

// Address is the response shape.
//
// The JSON keys are fixed by the mobile client, including the `lang` key for
// longitude: `CustomerAddressResponse.Lng` was tagged `json:"lang"` in the
// service being replaced and every client in the field decodes that key first.
// Renaming it here would silently zero the longitude on those installs.
type Address struct {
	ID          uuid.UUID `json:"id"`
	User        string    `json:"user"`
	Label       string    `json:"label"`
	AddressText string    `json:"address_text"`
	Lat         float64   `json:"lat"`
	Lng         float64   `json:"lang"`
	IsDefault   bool      `json:"is_default"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NearbyShop is one result of the proximity search.
//
// It is deliberately a *lean* projection of a shop. The endpoint it serves has
// an unauthenticated variant, and the previous implementation answered it with
// the full admin shop payload: every stocked product, every assigned cashier
// with their name, e-mail and photo, and the owner's name. None of that is
// something an anonymous caller should be handed, so only what the client
// actually renders on a shop card is selected here.
type NearbyShop struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Cover       string    `json:"cover"`
	Description string    `json:"description"`
	FullAddress string    `json:"full_address"`
	Lat         float64   `json:"lat"`
	Lng         float64   `json:"lang"`
	Status      int       `json:"status"`
	ShopImages  []string  `json:"shop_images"`
	// Distance is pre-formatted as "1.24 Km", the exact shape the clients
	// already parse.
	Distance string `json:"distance"`
}

// Input is a create or update request, already parsed and validated.
type Input struct {
	Label       string
	AddressText string
	Lat         float64
	Lng         float64
}

// Patch is an edit: only the fields the caller actually sent are non-nil.
//
// The edit endpoint has always been partial -- sending just a new label must
// not blank the street line -- so the absent/empty distinction has to survive
// as far as the service, which merges it onto the stored row.
type Patch struct {
	Label       *string
	AddressText *string
	Lat         *float64
	Lng         *float64
}

// Apply returns a complete Input: the stored values with the patch on top.
func (p Patch) Apply(current Address) Input {
	input := Input{
		Label:       current.Label,
		AddressText: current.AddressText,
		Lat:         current.Lat,
		Lng:         current.Lng,
	}
	if p.Label != nil {
		input.Label = *p.Label
	}
	if p.AddressText != nil {
		input.AddressText = *p.AddressText
	}
	if p.Lat != nil {
		input.Lat = *p.Lat
	}
	if p.Lng != nil {
		input.Lng = *p.Lng
	}
	return input
}

// Point is a geographic coordinate the nearby search measures from.
type Point struct {
	Lat float64
	Lng float64
}

// Valid reports whether the coordinate is inside the real range. Anything else
// makes the haversine expression return a value that is not a distance.
func (p Point) Valid() bool {
	return p.Lat >= -90 && p.Lat <= 90 && p.Lng >= -180 && p.Lng <= 180
}

// Repository is the persistence port.
//
// Every method that touches a single address takes the owner as a separate
// argument rather than trusting the id: ownership is enforced in the WHERE
// clause, not by a check the service could forget to perform.
type Repository interface {
	// ListByUser returns every address owned by userID, default first.
	ListByUser(ctx context.Context, userID uuid.UUID) ([]Address, error)
	// FindByID returns one address, or ErrNotFound if it is missing or owned
	// by somebody else.
	FindByID(ctx context.Context, id, userID uuid.UUID) (Address, error)
	// FindDefault returns the owner's default address, or ErrNotFound.
	FindDefault(ctx context.Context, userID uuid.UUID) (Address, error)
	// Create inserts an address. When the owner has none yet the new row
	// becomes the default, decided inside the same transaction so two
	// concurrent creates cannot both see an empty list.
	Create(ctx context.Context, userID uuid.UUID, input Input) (Address, error)
	// Update edits an address in place, ErrNotFound when it is not the
	// caller's.
	Update(ctx context.Context, id, userID uuid.UUID, input Input) (Address, error)
	// Delete removes an address and, when it was the default, promotes the
	// most recent remaining one in the same transaction.
	Delete(ctx context.Context, id, userID uuid.UUID) error
	// SetDefault promotes one address and demotes the rest atomically.
	SetDefault(ctx context.Context, id, userID uuid.UUID) error
	// NearbyShops returns active shops within radiusKM of origin, nearest
	// first, capped at limit. The ordering and the cap are the database's job.
	NearbyShops(ctx context.Context, origin Point, radiusKM float64, limit int) ([]NearbyShop, error)
}
