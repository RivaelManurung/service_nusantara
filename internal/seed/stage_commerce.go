package seed

import (
	"context"

	"service_nusantara/internal/model"
)

// seedShops writes outlets, their gallery, their stock and their cashiers.
func (s *Seeder) seedShops(ctx context.Context, _ Options) error {
	adminID := userID("admin")

	shops := make([]model.Shop, 0, len(seedShops))
	shopImages := make([]model.ShopImage, 0, len(seedShops))
	cashiers := make([]model.ShopCashier, 0, len(seedShops))

	for _, shop := range seedShops {
		shopID := id("shop", shop.Key)

		shops = append(shops, model.Shop{
			ID:            shopID,
			Name:          shop.Name,
			Cover:         assetURL(FolderShops, shop.Key+"-cover"),
			CoverPublicID: assetPublicID(FolderShops, shop.Key+"-cover"),
			Description:   shop.Description,
			FullAddress:   shop.Address,
			Lat:           shop.Lat,
			Lng:           shop.Lng,
			Status:        1,
			CreatedBy:     adminID,
		})

		shopImages = append(shopImages, model.ShopImage{
			ID:      id("shop-image", shop.Key),
			ShopID:  shopID,
			ImageID: id("image", "shop:"+shop.Key),
			Altext:  shop.Name,
		})

		if shop.CashierKey != "" {
			cashiers = append(cashiers, model.ShopCashier{
				ID:         id("shop-cashier", shop.Key),
				ShopID:     shopID,
				CashierID:  userID(shop.CashierKey),
				AssignedAt: s.daysFromNow(-120),
			})
		}
	}

	if err := upsert(ctx, s.db, shops); err != nil {
		return err
	}
	if err := upsert(ctx, s.db, shopImages); err != nil {
		return err
	}
	if err := upsert(ctx, s.db, cashiers); err != nil {
		return err
	}

	return s.seedShopProducts(ctx)
}

// seedShopProducts gives every outlet its own price and stock for a subset of
// the catalogue, so per-outlet pricing has something to exercise it.
func (s *Seeder) seedShopProducts(ctx context.Context) error {
	var rows []model.ShopProducts

	for shopIndex, shop := range seedShops {
		for productIndex, product := range seedProducts {
			// Each outlet skips a different slice of the catalogue, which
			// leaves realistic gaps instead of five identical inventories.
			if (productIndex+shopIndex)%4 == 3 {
				continue
			}

			// Outlets further from Java carry a small logistics premium.
			price := float64(product.Price) * (1 + float64(shopIndex)*0.02)
			stock := 12 + (productIndex*7+shopIndex*3)%140
			status := 1
			if stock < 20 {
				// Nearly empty shelves are marked inactive, giving the UI an
				// out-of-stock case to render.
				status = 0
			}

			rows = append(rows, model.ShopProducts{
				ID:         id("shop-product", shop.Key+":"+product.Key),
				ShopID:     id("shop", shop.Key),
				ProductID:  id("product", product.Key),
				Price:      price,
				Stock:      stock,
				Status:     status,
				AssignedAt: s.daysFromNow(-90),
			})
		}
	}

	return upsert(ctx, s.db, rows)
}

// customerKeys are the hand-written customer fixtures, in a stable order.
func customerKeys() []string {
	var keys []string
	for _, u := range seedUsers {
		if u.Role == roleCustomer {
			keys = append(keys, u.Key)
		}
	}
	return keys
}

func (s *Seeder) seedAddresses(ctx context.Context, _ Options) error {
	type place struct {
		label   string
		text    string
		lat     float64
		lng     float64
		primary bool
	}

	places := []place{
		{"Rumah", "Jl. Kaliurang KM 5 No. 21, Caturtunggal, Depok, Sleman, DIY 55281", -7.762860, 110.377420, true},
		{"Kantor", "Gedung Wisma Nusantara Lt. 8, Jl. Jend. Sudirman Kav. 21, Jakarta Selatan 12920", -6.216540, 106.821710, false},
		{"Kos", "Jl. Cisitu Lama No. 14, Dago, Coblong, Bandung 40135", -6.881230, 107.612340, false},
	}

	var rows []model.CustomerAddress
	for customerIndex, key := range customerKeys() {
		// Customers get one to three addresses, so both the single-address and
		// the pick-an-address screens have data.
		count := 1 + customerIndex%len(places)
		for i := range count {
			p := places[i]
			rows = append(rows, model.CustomerAddress{
				ID:          id("address", key+":"+p.label),
				UserID:      userID(key),
				Label:       p.label,
				AddressText: p.text,
				Lat:         p.lat,
				Lng:         p.lng,
				IsDefault:   p.primary,
			})
		}
	}

	return upsert(ctx, s.db, rows)
}

// seedCarts leaves each customer an open cart with a few selected items.
func (s *Seeder) seedCarts(ctx context.Context, _ Options) error {
	var carts []model.Cart
	var items []model.CartItem

	for customerIndex, key := range customerKeys() {
		shop := seedShops[customerIndex%len(seedShops)]
		cartID := id("cart", key+":open")

		carts = append(carts, model.Cart{
			ID:     cartID,
			UserID: userID(key),
			ShopID: id("shop", shop.Key),
			Status: 0, // open
		})

		for i := range 3 {
			product := seedProducts[(customerIndex*5+i*3)%len(seedProducts)]
			items = append(items, model.CartItem{
				ID:        id("cart-item", key+":"+product.Key),
				CartID:    cartID,
				ProductID: id("product", product.Key),
				// One item left unselected exercises partial checkout.
				Selected: i != 2,
			})
		}
	}

	if err := upsert(ctx, s.db, carts); err != nil {
		return err
	}
	return upsert(ctx, s.db, items)
}

func (s *Seeder) seedFavorites(ctx context.Context, _ Options) error {
	var favorites []model.Favorite
	var items []model.FavoriteItem

	for customerIndex, key := range customerKeys() {
		favoriteID := id("favorite", key)
		favorites = append(favorites, model.Favorite{ID: favoriteID, UserID: userID(key)})

		for i := range 4 {
			product := seedProducts[(customerIndex*7+i*5)%len(seedProducts)]
			items = append(items, model.FavoriteItem{
				ID:         id("favorite-item", key+":"+product.Key),
				FavoriteID: favoriteID,
				ProductID:  id("product", product.Key),
				Selected:   true,
			})
		}
	}

	if err := upsert(ctx, s.db, favorites); err != nil {
		return err
	}
	return upsert(ctx, s.db, items)
}
