package seed

import (
	"context"
	"time"

	"service_nusantara/internal/model"
)

func (s *Seeder) seedBanners(ctx context.Context, _ Options) error {
	adminID := userID("admin")

	banners := make([]model.Banner, 0, len(seedBanners))
	for i, b := range seedBanners {
		banners = append(banners, model.Banner{
			ID:          id("banner", b.Key),
			Name:        b.Name,
			Description: b.Description,
			Photo:       b.Photo,
			// The last banner is left inactive so the admin list has both states.
			Status: map[bool]int{true: 1, false: 0}[i < len(seedBanners)-1],
			UserID: adminID,
		})
	}

	return upsert(ctx, s.db, banners)
}

// seedVouchers writes the coupons and hands some of them to customers.
func (s *Seeder) seedVouchers(ctx context.Context, _ Options) error {
	adminID := userID("superadmin")

	vouchers := make([]model.Voucher, 0, len(seedVouchers))
	details := make([]model.UserVoucherDetail, 0, len(seedVouchers))

	for _, v := range seedVouchers {
		start := s.daysFromNow(v.StartOffsetDays)
		end := s.daysFromNow(v.EndOffsetDays)

		// An expired coupon is inactive; the fixture keeps one so redemption
		// rules have something to reject.
		status := 1
		if end.Before(s.now) {
			status = 0
		}

		vouchers = append(vouchers, model.Voucher{
			ID:              id("voucher", v.Key),
			Code:            v.Code,
			Description:     v.Description,
			DiscountType:    v.DiscountType,
			DiscountAmount:  v.DiscountAmount,
			DiscountPercent: v.DiscountPercent,
			MinimumSpend:    v.MinimumSpend,
			PointCost:       v.PointCost,
			StartDate:       start,
			EndDate:         end,
			Quota:           v.Quota,
			ClaimedCount:    v.Quota / 10,
			Status:          status,
			CreatedBy:       adminID,
		})

		// The snapshot is what a claimed voucher freezes, so editing the
		// coupon later cannot change a coupon someone already holds.
		details = append(details, model.UserVoucherDetail{
			ID:                id("voucher-detail", v.Key),
			VoucherCode:       v.Code,
			DiscountType:      v.DiscountType,
			DiscountAmount:    v.DiscountAmount,
			DiscountPercent:   v.DiscountPercent,
			MinPurchaseAmount: v.MinimumSpend,
			ValidFrom:         start,
			ValidUntil:        end,
			Description:       v.Description,
		})
	}

	if err := upsert(ctx, s.db, vouchers); err != nil {
		return err
	}
	if err := upsert(ctx, s.db, details); err != nil {
		return err
	}

	return s.seedClaimedVouchers(ctx)
}

func (s *Seeder) seedClaimedVouchers(ctx context.Context) error {
	var claimed []model.UserVoucher

	for customerIndex, key := range customerKeys() {
		// Each customer holds two coupons, one already spent.
		for i := range 2 {
			v := seedVouchers[(customerIndex*2+i)%len(seedVouchers)]
			used := i == 1

			row := model.UserVoucher{
				ID:        id("user-voucher", key+":"+v.Key),
				UserID:    userID(key),
				VoucherID: id("voucher", v.Key),
				DetailID:  id("voucher-detail", v.Key),
				IsUsed:    used,
			}
			if used {
				row.RedeemedAt = ptr(s.daysFromNow(-3))
			}

			claimed = append(claimed, row)
		}
	}

	return upsert(ctx, s.db, claimed)
}

// seedPoints gives every customer a loyalty balance backed by a ledger.
func (s *Seeder) seedPoints(ctx context.Context, _ Options) error {
	var balances []model.UserPoint
	var ledger []model.UserPointHistories

	type entry struct {
		suffix      string
		pointType   string
		source      string
		points      int
		direction   string
		description string
		daysAgo     int
	}

	entries := []entry{
		{"signup", "reward", "registration", 100, "in", "Bonus pendaftaran akun baru", 90},
		{"purchase-1", "purchase", "order", 240, "in", "Poin dari pembelian oleh-oleh", 45},
		{"purchase-2", "purchase", "order", 180, "in", "Poin dari pembelian hampers", 20},
		{"exchange", "exchange", "voucher", 500, "out", "Tukar poin dengan voucher Rp50.000", 10},
		{"birthday", "reward", "campaign", 150, "in", "Hadiah ulang tahun pelanggan", 5},
	}

	for _, key := range customerKeys() {
		total := 0
		for _, e := range entries {
			if e.direction == "in" {
				total += e.points
			} else {
				total -= e.points
			}

			// Earned points expire after a year; spent ones never do.
			var expiry *time.Time
			if e.direction == "in" {
				expiry = ptr(s.daysFromNow(365 - e.daysAgo))
			}

			ledger = append(ledger, model.UserPointHistories{
				ID:          id("point-history", key+":"+e.suffix),
				UserID:      userID(key),
				PointType:   e.pointType,
				Source:      e.source,
				SourceId:    id("point-source", key+":"+e.suffix).String(),
				Points:      e.points,
				Direction:   e.direction,
				Description: e.description,
				ExpiredAt:   expiry,
			})
		}

		balances = append(balances, model.UserPoint{
			ID:          id("point", key),
			UserID:      userID(key),
			TotalPoints: total,
		})
	}

	if err := upsert(ctx, s.db, balances); err != nil {
		return err
	}
	return upsert(ctx, s.db, ledger)
}

// seedEvents writes one percentage-discount campaign and one buy-and-get bundle.
func (s *Seeder) seedEvents(ctx context.Context, _ Options) error {
	adminID := userID("admin")

	events := []model.Event{
		{
			ID:        id("event", "pekan-kopi"),
			Name:      "Pekan Kopi Nusantara",
			TypeEvent: model.EventTypeDiskon,
			StartDate: s.daysFromNow(-5),
			EndDate:   s.daysFromNow(9),
			Cover:     "https://cdn.nusantara.test/events/pekan-kopi.jpg",
			Status:    1,
			CreatedBy: adminID,
		},
		{
			ID:        id("event", "paket-hampers"),
			Name:      "Paket Hampers Lebaran",
			TypeEvent: model.EventTypeBundle,
			StartDate: s.daysFromNow(-2),
			EndDate:   s.daysFromNow(28),
			Cover:     "https://cdn.nusantara.test/events/hampers.jpg",
			Status:    1,
			CreatedBy: adminID,
		},
		{
			// A finished campaign, so "past events" is not an empty screen.
			ID:        id("event", "batik-day"),
			Name:      "Hari Batik Nasional",
			TypeEvent: model.EventTypeDiskon,
			StartDate: s.daysFromNow(-60),
			EndDate:   s.daysFromNow(-45),
			Cover:     "https://cdn.nusantara.test/events/batik.jpg",
			Status:    0,
			CreatedBy: adminID,
		},
	}
	if err := upsert(ctx, s.db, events); err != nil {
		return err
	}

	// Percentage discounts on the coffee range.
	discounted := []struct {
		event   string
		product string
		percent int
	}{
		{"pekan-kopi", "kopi-gayo", 20},
		{"pekan-kopi", "kopi-toraja", 20},
		{"pekan-kopi", "kopi-luwak", 15},
		{"pekan-kopi", "teh-poci", 10},
		{"batik-day", "batik-tulis", 15},
		{"batik-day", "batik-cap", 10},
		{"batik-day", "tenun-ikat", 10},
	}

	eventProducts := make([]model.EventProduct, 0, len(discounted))
	for _, d := range discounted {
		eventProducts = append(eventProducts, model.EventProduct{
			ID:              id("event-product", d.event+":"+d.product),
			EventID:         id("event", d.event),
			ProductID:       id("product", d.product),
			DiscountPercent: d.percent,
		})
	}
	if err := upsert(ctx, s.db, eventProducts); err != nil {
		return err
	}

	// Buy three boxes of pastry, get a jar of biscuits and a tea pack free.
	buys := []struct {
		product  string
		quantity int
	}{
		{"bakpia-25-keju", 2},
		{"bakpia-25-kacang", 1},
		{"nastar-keju", 1},
	}
	bundleBuys := make([]model.EventBundleBuy, 0, len(buys))
	for _, b := range buys {
		bundleBuys = append(bundleBuys, model.EventBundleBuy{
			ID:        id("bundle-buy", "paket-hampers:"+b.product),
			EventID:   id("event", "paket-hampers"),
			ProductID: id("product", b.product),
			Quantity:  b.quantity,
		})
	}
	if err := upsert(ctx, s.db, bundleBuys); err != nil {
		return err
	}

	rewards := []struct {
		product  string
		quantity int
	}{
		{"teh-poci", 1},
		{"gantungan-kunci", 2},
	}
	bundleRewards := make([]model.EventBundleReward, 0, len(rewards))
	for _, rw := range rewards {
		bundleRewards = append(bundleRewards, model.EventBundleReward{
			ID:        id("bundle-reward", "paket-hampers:"+rw.product),
			EventID:   id("event", "paket-hampers"),
			ProductID: id("product", rw.product),
			Quantity:  rw.quantity,
		})
	}
	return upsert(ctx, s.db, bundleRewards)
}
