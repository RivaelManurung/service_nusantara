package model

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// All returns every persisted model, in dependency order.
//
// Keeping the list in the model package means adding a table cannot be
// forgotten in a config package on the other side of the tree.
func All() []any {
	return []any{
		&Role{}, &User{}, &UserIdentity{},
		&Image{},
		&Banner{}, &TypeProduct{},
		&UserPoint{}, &UserPointHistories{},
		&Voucher{}, &UserVoucher{}, &UserVoucherDetail{},
		&Product{}, &ProductImage{},
		&Shop{}, &ShopCashier{}, &ShopProducts{}, &ShopImage{},
		&CustomerAddress{},
		&Event{}, &EventProduct{}, &EventBundleBuy{}, &EventBundleReward{},
		&Cart{}, &CartItem{},
		&Favorite{}, &FavoriteItem{},
		&Order{}, &OrderItem{}, &OrderEvent{}, &OrderReward{}, &OrderVoucher{},
		&Notification{},
	}
}

// AutoMigrate syncs the schema. It is intended for development only: production
// deployments run reviewed SQL migrations, because AutoMigrate never drops or
// renames and silently diverges from what the code expects.
func AutoMigrate(ctx context.Context, db *gorm.DB) error {
	if err := db.WithContext(ctx).AutoMigrate(All()...); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}
	return nil
}
