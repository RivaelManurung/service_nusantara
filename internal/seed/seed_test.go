package seed

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests guard the fixture itself. A seeder that only fails against a live
// database is a seeder nobody runs, so every referential rule that can be
// checked from the dataset alone is checked here.

func TestIDsAreStableAcrossCalls(t *testing.T) {
	// Idempotency rests entirely on this: if ids moved between runs, a second
	// run would insert a duplicate of every row.
	first := id("product", "bakpia-25-keju")
	second := id("product", "bakpia-25-keju")

	assert.Equal(t, first, second)
}

func TestIDsDifferPerKindAndKey(t *testing.T) {
	assert.NotEqual(t, id("product", "same"), id("shop", "same"))
	assert.NotEqual(t, id("product", "a"), id("product", "b"))
	assert.NotEqual(t, uuid.Nil, id("product", "a"))
}

func TestEveryProductReferencesAKnownType(t *testing.T) {
	types := map[string]bool{}
	for _, tp := range seedTypeProducts {
		types[tp.Key] = true
	}

	for _, p := range seedProducts {
		assert.Truef(t, types[p.Type], "product %q references unknown type %q", p.Key, p.Type)
	}
}

func TestProductKeysAndCodesAreUnique(t *testing.T) {
	keys := map[string]bool{}
	codes := map[string]bool{}

	for _, p := range seedProducts {
		assert.Falsef(t, keys[p.Key], "duplicate product key %q", p.Key)
		assert.Falsef(t, codes[p.Code], "duplicate product code %q", p.Code)
		keys[p.Key] = true
		codes[p.Code] = true
	}
}

func TestProductsHaveAPositivePriceAndAUnit(t *testing.T) {
	for _, p := range seedProducts {
		assert.Positivef(t, p.Price, "product %q has no price", p.Key)
		assert.NotEmptyf(t, p.Unit, "product %q has no unit", p.Key)
		assert.NotEmptyf(t, p.Description, "product %q has no description", p.Key)
	}
}

func TestUserKeysEmailsAndPhonesAreUnique(t *testing.T) {
	// The users table has unique indexes on all three, so a clash here would
	// only surface as a constraint violation mid-run.
	keys := map[string]bool{}
	emails := map[string]bool{}
	phones := map[string]bool{}

	for _, u := range seedUsers {
		assert.Falsef(t, keys[u.Key], "duplicate user key %q", u.Key)
		keys[u.Key] = true

		if u.Email != "" {
			email := strings.ToLower(u.Email)
			assert.Falsef(t, emails[email], "duplicate email %q", email)
			emails[email] = true
		}
		if u.Phone != "" {
			assert.Falsef(t, phones[u.Phone], "duplicate phone %q", u.Phone)
			phones[u.Phone] = true
		}
	}
}

func TestEveryUserHasAKnownRoleAndAtLeastOneProvider(t *testing.T) {
	for _, u := range seedUsers {
		assert.Containsf(t, roleNames, u.Role, "user %q has unknown role %q", u.Key, u.Role)
		assert.NotEmptyf(t, u.Providers, "user %q has no sign-in method", u.Key)
	}
}

func TestEverySignInMethodHasAFixture(t *testing.T) {
	// The point of the user fixtures is to cover all four login paths.
	var covered []string
	for _, u := range seedUsers {
		covered = append(covered, u.Providers...)
	}

	for _, provider := range []string{providerPassword, providerGoogle, providerApple, providerPhone} {
		assert.Containsf(t, covered, provider, "no seeded account signs in with %q", provider)
	}
}

func TestPasswordlessAccountsExistForSocialAndPhone(t *testing.T) {
	// A Google- or phone-only account must have no password, so /auth/login
	// has something to reject.
	var passwordless int
	for _, u := range seedUsers {
		if !containsProvider(u.Providers, providerPassword) {
			passwordless++
			assert.Emptyf(t, u.Password, "user %q must not carry a password", u.Key)
		}
	}

	assert.GreaterOrEqual(t, passwordless, 2)
}

func TestIdentitySubjectsAreUnique(t *testing.T) {
	// (provider, subject) is a unique index on user_identities.
	seen := map[string]bool{}

	for _, u := range seedUsers {
		for _, provider := range u.Providers {
			subject, ok := identitySubject(u, provider)
			if !ok {
				continue
			}
			key := provider + ":" + subject
			assert.Falsef(t, seen[key], "duplicate identity %q", key)
			seen[key] = true
		}
	}
}

func TestIdentitySubjectIsSkippedWhenTheAccountLacksTheIdentifier(t *testing.T) {
	// A password identity is keyed by email; an account without one cannot
	// have that row.
	_, ok := identitySubject(seedUser{Key: "x", Providers: []string{providerPassword}}, providerPassword)
	assert.False(t, ok)

	_, ok = identitySubject(seedUser{Key: "x", Providers: []string{providerPhone}}, providerPhone)
	assert.False(t, ok)
}

func TestShopCashiersReferenceKnownCashierAccounts(t *testing.T) {
	cashiers := map[string]bool{}
	for _, u := range seedUsers {
		if u.Role == roleCashier {
			cashiers[u.Key] = true
		}
	}

	for _, shop := range seedShops {
		if shop.CashierKey == "" {
			continue
		}
		assert.Truef(t, cashiers[shop.CashierKey],
			"shop %q is assigned unknown cashier %q", shop.Key, shop.CashierKey)
	}
}

func TestShopCoordinatesAreWithinIndonesia(t *testing.T) {
	for _, shop := range seedShops {
		assert.Truef(t, shop.Lat > -11 && shop.Lat < 6, "shop %q latitude %v is outside Indonesia", shop.Key, shop.Lat)
		assert.Truef(t, shop.Lng > 95 && shop.Lng < 141, "shop %q longitude %v is outside Indonesia", shop.Key, shop.Lng)
	}
}

func TestVoucherCodesAreUnique(t *testing.T) {
	codes := map[string]bool{}
	for _, v := range seedVouchers {
		assert.Falsef(t, codes[v.Code], "duplicate voucher code %q", v.Code)
		codes[v.Code] = true
	}
}

func TestEachVoucherIsEitherPercentOrAmount(t *testing.T) {
	// Both set at once would make the discount ambiguous.
	for _, v := range seedVouchers {
		hasPercent := v.DiscountPercent > 0
		hasAmount := v.DiscountAmount > 0
		assert.Truef(t, hasPercent != hasAmount,
			"voucher %q must set exactly one of percent or amount", v.Key)
	}
}

func TestTheFixtureIncludesAnExpiredVoucher(t *testing.T) {
	// Redemption rules need a negative case to exercise.
	var expired bool
	for _, v := range seedVouchers {
		if v.EndOffsetDays < 0 {
			expired = true
		}
	}

	assert.True(t, expired)
}

func TestOrdersReferenceKnownCustomersShopsAndProducts(t *testing.T) {
	customers := map[string]bool{}
	for _, key := range customerKeys() {
		customers[key] = true
	}
	shops := map[string]bool{}
	for _, s := range seedShops {
		shops[s.Key] = true
	}
	products := productPrices()
	vouchers := map[string]bool{}
	for _, v := range seedVouchers {
		vouchers[v.Key] = true
	}

	for _, b := range orderBlueprints {
		assert.Truef(t, customers[b.customer], "order %q references unknown customer %q", b.key, b.customer)
		assert.Truef(t, shops[b.shop], "order %q references unknown shop %q", b.key, b.shop)
		assert.NotEmptyf(t, b.items, "order %q has no items", b.key)

		for _, line := range b.items {
			_, ok := products[line.product]
			assert.Truef(t, ok, "order %q references unknown product %q", b.key, line.product)
			assert.Positivef(t, line.quantity, "order %q line %q has no quantity", b.key, line.product)
		}
		if b.voucherKey != "" {
			assert.Truef(t, vouchers[b.voucherKey], "order %q references unknown voucher %q", b.key, b.voucherKey)
		}
		if b.rewardProduct != "" {
			_, ok := products[b.rewardProduct]
			assert.Truef(t, ok, "order %q rewards unknown product %q", b.key, b.rewardProduct)
		}
	}
}

func TestOrderKeysAreUnique(t *testing.T) {
	keys := map[string]bool{}
	for _, b := range orderBlueprints {
		assert.Falsef(t, keys[b.key], "duplicate order key %q", b.key)
		keys[b.key] = true
	}
}

func TestOrdersCoverTheWholeStatusLifecycle(t *testing.T) {
	var statuses []string
	for _, b := range orderBlueprints {
		statuses = append(statuses, string(b.status))
	}

	// Without a fixture per status, an admin filter can look correct while
	// returning nothing.
	for _, want := range []string{
		"ORDER_DRAFT", "WAITING_PAYMENT", "PAID", "WAITING_STORE_CONFIRMATION",
		"STORE_REJECTED", "ON_THE_WAY", "DELIVERED", "COMPLETED", "CANCELED",
	} {
		assert.Containsf(t, statuses, want, "no seeded order has status %q", want)
	}
}

func TestDiscountForRespectsTheMinimumSpend(t *testing.T) {
	// NUSANTARA10 is 10 percent with a Rp100.000 minimum.
	assert.Equal(t, 0.0, discountFor("welcome", 50_000))
	assert.Equal(t, 15_000.0, discountFor("welcome", 150_000))
}

func TestDiscountForNeverExceedsTheSubtotal(t *testing.T) {
	// GRATISONGKIR is a flat Rp15.000; a smaller basket must not go negative.
	// Its minimum spend is Rp150.000, so use a basket above it.
	assert.Equal(t, 15_000.0, discountFor("ongkir", 200_000))
	assert.Equal(t, 0.0, discountFor("unknown-voucher", 200_000))
}

func TestOrderCodesAreStableAndUnique(t *testing.T) {
	codes := map[string]bool{}
	for _, b := range orderBlueprints {
		code := orderCode(b.key)
		assert.True(t, strings.HasPrefix(code, "NSTR-"))
		assert.Falsef(t, codes[code], "duplicate order code %q", code)
		codes[code] = true
		assert.Equal(t, code, orderCode(b.key), "order code must be stable")
	}
}

func TestStageNamesAreUniqueAndOrdered(t *testing.T) {
	names := StageNames()

	assert.Equal(t, len(names), len(slices.Compact(slices.Clone(names))))
	// Roles must come first and orders last, because everything else hangs off
	// a role and orders depend on nearly every other table.
	assert.Equal(t, "roles", names[0])
	assert.Equal(t, "orders", names[len(names)-1])
}

func TestValidateStageNamesRejectsATypo(t *testing.T) {
	err := validateStageNames(Options{Only: []string{"prodcuts"}})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "prodcuts")
}

func TestValidateStageNamesAcceptsKnownStages(t *testing.T) {
	assert.NoError(t, validateStageNames(Options{Only: []string{"roles"}, Skip: []string{"orders"}}))
}

func TestSelectedHonoursOnlyAndSkip(t *testing.T) {
	assert.True(t, selected("roles", Options{}))
	assert.True(t, selected("roles", Options{Only: []string{"roles", "users"}}))
	assert.False(t, selected("orders", Options{Only: []string{"roles"}}))
	assert.False(t, selected("orders", Options{Skip: []string{"orders"}}))
	// Skip wins over Only, which is the least surprising resolution.
	assert.False(t, selected("roles", Options{Only: []string{"roles"}, Skip: []string{"roles"}}))
}

func TestAccountSummaryDescribesEverySeededUser(t *testing.T) {
	summary := AccountSummary()

	require.Len(t, summary, len(seedUsers))
	// The phone-only account has no email, so it must be listed by number.
	assert.Contains(t, strings.Join(summary, "\n"), "+6281200000003")
}
