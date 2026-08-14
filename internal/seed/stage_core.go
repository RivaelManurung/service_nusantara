package seed

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"service_nusantara/internal/model"
)

func (s *Seeder) seedRoles(ctx context.Context, _ Options) error {
	roles := make([]model.Role, 0, len(roleNames))
	for _, name := range roleNames {
		roles = append(roles, model.Role{ID: id("role", name), Name: name})
	}
	return upsert(ctx, s.db, roles)
}

func (s *Seeder) seedUsers(ctx context.Context, opts Options) error {
	// Hashing dominates the runtime of this stage, so the same password is
	// hashed once and shared rather than re-derived per account.
	hash, err := s.hasher.Hash(demoPassword)
	if err != nil {
		return fmt.Errorf("hash demo password: %w", err)
	}

	users := make([]model.User, 0, len(seedUsers)+opts.Scale*generatedCustomers)
	for _, u := range seedUsers {
		users = append(users, s.buildUser(u, hash))
	}
	users = append(users, s.generatedCustomers(opts, hash)...)

	return upsert(ctx, s.db, users)
}

func (s *Seeder) buildUser(u seedUser, hash string) model.User {
	row := model.User{
		ID:            id("user", u.Key),
		Name:          u.Name,
		Username:      optional(u.Username),
		Email:         optional(strings.ToLower(u.Email)),
		Phone:         optional(u.Phone),
		Gender:        u.Gender,
		RoleID:        id("role", u.Role),
		Status:        1,
		DateOfBirth:   ptr(s.now.AddDate(-30, 0, 0)),
		Photo:         assetURL(FolderAvatars, u.Key),
		PhotoPublicID: assetPublicID(FolderAvatars, u.Key),
	}

	// Only accounts that list the password provider get a hash; the others
	// must be rejected by /auth/login, which is the behaviour worth having a
	// fixture for.
	if containsProvider(u.Providers, providerPassword) {
		row.Password = ptr(hash)
	}
	if row.Email != nil {
		row.EmailVerifiedAt = ptr(s.now.AddDate(0, -2, 0))
	}
	if row.Phone != nil && containsProvider(u.Providers, providerPhone) {
		row.PhoneVerifiedAt = ptr(s.now.AddDate(0, -1, 0))
	}

	return row
}

// generatedCustomers is how many extra customers each scale step adds.
const generatedCustomers = 20

// generatedCustomers builds bulk accounts for list and pagination testing.
func (s *Seeder) generatedCustomers(opts Options, hash string) []model.User {
	total := (opts.Scale - 1) * generatedCustomers
	if total <= 0 {
		return nil
	}

	users := make([]model.User, 0, total)
	for i := range total {
		key := fmt.Sprintf("generated.customer.%04d", i)
		users = append(users, model.User{
			ID:              id("user", key),
			Name:            fmt.Sprintf("Pelanggan Demo %04d", i+1),
			Username:        ptr(fmt.Sprintf("pelanggan%04d", i+1)),
			Email:           ptr(fmt.Sprintf("pelanggan%04d@nusantara.test", i+1)),
			Phone:           ptr(fmt.Sprintf("+62899%07d", i+1)),
			Password:        ptr(hash),
			EmailVerifiedAt: ptr(s.now.AddDate(0, 0, -i%90)),
			RoleID:          id("role", roleCustomer),
			Status:          1,
		})
	}
	return users
}

func (s *Seeder) seedIdentities(ctx context.Context, _ Options) error {
	var identities []model.UserIdentity

	for _, u := range seedUsers {
		for _, provider := range u.Providers {
			subject, ok := identitySubject(u, provider)
			if !ok {
				continue
			}

			identities = append(identities, model.UserIdentity{
				ID:          id("identity", u.Key+":"+provider),
				UserID:      id("user", u.Key),
				Provider:    provider,
				Subject:     subject,
				Email:       strings.ToLower(u.Email),
				LastLoginAt: ptr(s.now.AddDate(0, 0, -1)),
			})
		}
	}

	return upsert(ctx, s.db, identities)
}

// identitySubject derives the provider's stable identifier for an account. For
// Google and Apple the real value comes from the `sub` claim; the seeder mints
// a deterministic stand-in so the fixture is reproducible.
func identitySubject(u seedUser, provider string) (string, bool) {
	switch provider {
	case providerPassword:
		if u.Email == "" {
			return "", false
		}
		return strings.ToLower(u.Email), true
	case providerPhone:
		if u.Phone == "" {
			return "", false
		}
		return u.Phone, true
	case providerGoogle, providerApple:
		return fmt.Sprintf("%s-seed-%s", provider, id("subject", u.Key+provider).String()[:18]), true
	default:
		return "", false
	}
}

func containsProvider(providers []string, want string) bool {
	for _, p := range providers {
		if p == want {
			return true
		}
	}
	return false
}

func (s *Seeder) seedImages(ctx context.Context, _ Options) error {
	images := make([]model.Image, 0, len(seedProducts)*2+len(seedShops))

	for _, p := range seedProducts {
		images = append(images,
			model.Image{
				ID:        id("image", "product:"+p.Key),
				ImagePath: assetURL(FolderProducts, p.Key),
				PublicID:  assetPublicID(FolderProducts, p.Key),
			},
			model.Image{
				ID:        id("image", "product-alt:"+p.Key),
				ImagePath: assetURL(FolderProducts, p.Key+"-2"),
				PublicID:  assetPublicID(FolderProducts, p.Key+"-2"),
			})
	}

	for _, shop := range seedShops {
		images = append(images, model.Image{
			ID:        id("image", "shop:"+shop.Key),
			ImagePath: assetURL(FolderShops, shop.Key),
			PublicID:  assetPublicID(FolderShops, shop.Key),
		})
	}

	return upsert(ctx, s.db, images)
}

// seedCatalog writes product types, products and their gallery rows.
func (s *Seeder) seedCatalog(ctx context.Context, _ Options) error {
	adminID := id("user", "admin")

	types := make([]model.TypeProduct, 0, len(seedTypeProducts))
	for _, t := range seedTypeProducts {
		types = append(types, model.TypeProduct{
			ID:            id("type", t.Key),
			Name:          t.Name,
			Image:         assetURL(FolderTypes, t.Key),
			ImagePublicID: assetPublicID(FolderTypes, t.Key),
			Status:        1,
			UserID:        adminID,
		})
	}
	if err := upsert(ctx, s.db, types); err != nil {
		return err
	}

	products := make([]model.Product, 0, len(seedProducts))
	gallery := make([]model.ProductImage, 0, len(seedProducts)*2)

	for _, p := range seedProducts {
		productID := id("product", p.Key)
		products = append(products, model.Product{
			ID:            productID,
			Name:          p.Name,
			Code:          p.Code,
			Price:         p.Price,
			Unit:          p.Unit,
			Description:   p.Description,
			Status:        1,
			ImageID:       id("image", "product:"+p.Key),
			TypeProductID: id("type", p.Type),
			CreatedBy:     adminID,
		})

		for suffix, imageKey := range map[string]string{
			"primary": "product:" + p.Key,
			"alt":     "product-alt:" + p.Key,
		} {
			gallery = append(gallery, model.ProductImage{
				ID:        id("product-image", p.Key+":"+suffix),
				ProductID: productID,
				ImageID:   id("image", imageKey),
				AltText:   p.Name,
			})
		}
	}

	if err := upsert(ctx, s.db, products); err != nil {
		return err
	}
	return upsert(ctx, s.db, gallery)
}

// optional maps an empty string to NULL so unique indexes are not violated by
// several accounts that simply have no username or phone.
func optional(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

// daysFromNow is a small helper for the many relative timestamps below.
func (s *Seeder) daysFromNow(days int) time.Time { return s.now.AddDate(0, 0, days) }

// userID resolves a fixture key to its account id.
func userID(key string) uuid.UUID { return id("user", key) }
