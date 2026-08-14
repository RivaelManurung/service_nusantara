package seed

import (
	"context"
	"fmt"
	"math"

	"github.com/google/uuid"

	"service_nusantara/internal/model"
)

// orderBlueprint describes one order to build. Totals are computed from the
// lines rather than written by hand, so the fixture cannot drift into a state
// the application would consider corrupt.
type orderBlueprint struct {
	key           string
	customer      string
	shop          string
	status        model.OrderStatus
	orderType     model.OrderType
	payment       model.PaymentMethod
	daysAgo       int
	items         []orderLine
	voucherKey    string
	eventKey      string
	eventDiscount float64
	rewardProduct string
	rewardQty     int
	note          string
	withAddress   bool
}

type orderLine struct {
	product  string
	quantity int
}

// orderBlueprints cover every status in the lifecycle, both fulfilment types
// and all three payment methods, so no screen has an empty state by accident.
var orderBlueprints = []orderBlueprint{
	{
		key: "budi-completed", customer: "customer.budi", shop: "malioboro",
		status: model.OrderCompleted, orderType: model.Delivery, payment: model.PayQRIS,
		daysAgo: 21, withAddress: true, voucherKey: "welcome",
		items: []orderLine{{"bakpia-25-keju", 2}, {"gudeg-kaleng", 1}, {"kopi-gayo", 1}},
		note:  "Tolong dibungkus rapi untuk oleh-oleh kantor.",
	},
	{
		key: "budi-delivered", customer: "customer.budi", shop: "malioboro",
		status: model.OrderDelivered, orderType: model.Delivery, payment: model.PayTF,
		daysAgo: 6, withAddress: true,
		items: []orderLine{{"lapis-legit", 1}, {"nastar-keju", 2}},
	},
	{
		key: "siti-on-the-way", customer: "customer.siti", shop: "braga",
		status: model.OrderOnTheWay, orderType: model.Delivery, payment: model.PayQRIS,
		daysAgo: 0, withAddress: true,
		eventKey: "pekan-kopi", eventDiscount: 31200,
		items: []orderLine{{"kopi-gayo", 1}, {"kopi-toraja", 1}, {"teh-poci", 2}},
	},
	{
		key: "siti-paid", customer: "customer.siti", shop: "braga",
		status: model.OrderPaid, orderType: model.TakeAway, payment: model.PayCash,
		daysAgo: 2,
		items:   []orderLine{{"keripik-tempe", 3}, {"keripik-pisang", 2}},
	},
	{
		key: "dewi-waiting-store", customer: "customer.dewi", shop: "kuta",
		status: model.OrderWaitingStore, orderType: model.Delivery, payment: model.PayTF,
		daysAgo: 1, withAddress: true,
		items: []orderLine{{"kopi-luwak", 1}, {"tas-rotan", 1}},
	},
	{
		key: "dewi-bundle", customer: "customer.dewi", shop: "kuta",
		status: model.OrderCompleted, orderType: model.TakeAway, payment: model.PayCash,
		daysAgo:  12,
		eventKey: "paket-hampers", eventDiscount: 0,
		rewardProduct: "teh-poci", rewardQty: 1,
		items: []orderLine{{"bakpia-25-keju", 2}, {"bakpia-25-kacang", 1}, {"nastar-keju", 1}},
	},
	{
		key: "agus-waiting-payment", customer: "customer.agus", shop: "tunjungan",
		status: model.OrderWaitingPayment, orderType: model.Delivery, payment: model.PayQRIS,
		daysAgo: 0, withAddress: true,
		items: []orderLine{{"sambal-roa", 2}, {"kerupuk-udang", 1}},
	},
	{
		key: "agus-canceled", customer: "customer.agus", shop: "tunjungan",
		status: model.OrderCanceled, orderType: model.Delivery, payment: model.PayTF,
		daysAgo: 30, withAddress: true,
		items: []orderLine{{"batik-tulis", 1}},
		note:  "Dibatalkan pembeli, stok motif tidak tersedia.",
	},
	{
		key: "budi-store-rejected", customer: "customer.budi", shop: "kesawan",
		status: model.OrderStoreRejected, orderType: model.Delivery, payment: model.PayCash,
		daysAgo: 40, withAddress: true,
		items: []orderLine{{"wingko-babat", 4}},
		note:  "Ditolak outlet, stok habis.",
	},
	{
		key: "siti-draft", customer: "customer.siti", shop: "malioboro",
		status: model.OrderDraft, orderType: model.TakeAway, payment: model.PayCash,
		daysAgo: 0,
		items:   []orderLine{{"gantungan-kunci", 5}},
	},
}

// shippingFee is flat per delivery order in this fixture.
const shippingFee = 15000.0

func (s *Seeder) seedOrders(ctx context.Context, _ Options) error {
	var (
		carts    []model.Cart
		orders   []model.Order
		items    []model.OrderItem
		events   []model.OrderEvent
		rewards  []model.OrderReward
		vouchers []model.OrderVoucher
	)

	priceOf := productPrices()

	for _, b := range orderBlueprints {
		orderID := id("order", b.key)
		cartID := id("cart", "order:"+b.key)

		// Every order references the cart it came from, so the checkout path
		// has a complete history to read back.
		carts = append(carts, model.Cart{
			ID:     cartID,
			UserID: userID(b.customer),
			ShopID: id("shop", b.shop),
			Status: 1, // converted
		})

		subTotal := 0.0
		for _, line := range b.items {
			price, ok := priceOf[line.product]
			if !ok {
				return fmt.Errorf("order %q references unknown product %q", b.key, line.product)
			}

			lineTotal := float64(price * line.quantity)
			subTotal += lineTotal

			items = append(items, model.OrderItem{
				ID:        id("order-item", b.key+":"+line.product),
				OrderID:   orderID,
				ProductID: id("product", line.product),
				Quantity:  line.quantity,
				SubTotal:  lineTotal,
			})
		}

		voucherDiscount := 0.0
		if b.voucherKey != "" {
			voucherDiscount = discountFor(b.voucherKey, subTotal)
			vouchers = append(vouchers, model.OrderVoucher{
				ID:        id("order-voucher", b.key),
				OrderID:   orderID,
				VoucherID: id("voucher", b.voucherKey),
			})
		}

		if b.eventKey != "" {
			events = append(events, model.OrderEvent{
				ID:       id("order-event", b.key),
				OrderID:  orderID,
				EventID:  id("event", b.eventKey),
				Type:     eventTypeOf(b.eventKey),
				Discount: b.eventDiscount,
			})
		}

		if b.rewardProduct != "" {
			rewards = append(rewards, model.OrderReward{
				ID:        id("order-reward", b.key),
				OrderID:   orderID,
				ProductID: id("product", b.rewardProduct),
				Quantity:  b.rewardQty,
			})
		}

		shipping := 0.0
		if b.orderType == model.Delivery {
			shipping = shippingFee
		}

		total := subTotal - b.eventDiscount - voucherDiscount + shipping
		// A discount must never make an order payable in reverse.
		total = math.Max(total, 0)

		order := model.Order{
			ID:              orderID,
			Code:            orderCode(b.key),
			UserID:          userID(b.customer),
			ShopID:          id("shop", b.shop),
			CartID:          cartID,
			Status:          b.status,
			OrderType:       b.orderType,
			PaymentMethod:   b.payment,
			SubTotal:        subTotal,
			DiscountEvent:   b.eventDiscount,
			DiscountVoucher: voucherDiscount,
			ShippingFee:     shipping,
			Total:           total,
			CreatedAt:       s.daysFromNow(-b.daysAgo),
		}
		if b.note != "" {
			order.Note = ptr(b.note)
		}
		if b.withAddress {
			order.CustomerAddressID = ptr(id("address", b.customer+":Rumah"))
		}

		orders = append(orders, order)
	}

	if err := upsert(ctx, s.db, carts); err != nil {
		return err
	}
	if err := upsert(ctx, s.db, orders); err != nil {
		return err
	}
	if err := upsert(ctx, s.db, items); err != nil {
		return err
	}
	if err := upsert(ctx, s.db, events); err != nil {
		return err
	}
	if err := upsert(ctx, s.db, rewards); err != nil {
		return err
	}
	return upsert(ctx, s.db, vouchers)
}

// productPrices indexes the catalogue by key.
func productPrices() map[string]int {
	prices := make(map[string]int, len(seedProducts))
	for _, p := range seedProducts {
		prices[p.Key] = p.Price
	}
	return prices
}

// discountFor applies a seeded voucher to a subtotal, honouring its minimum
// spend so the fixture matches what the redemption rules would produce.
func discountFor(voucherKey string, subTotal float64) float64 {
	for _, v := range seedVouchers {
		if v.Key != voucherKey {
			continue
		}
		if subTotal < float64(v.MinimumSpend) {
			return 0
		}
		if v.DiscountPercent > 0 {
			return math.Round(subTotal * float64(v.DiscountPercent) / 100)
		}
		return math.Min(float64(v.DiscountAmount), subTotal)
	}
	return 0
}

func eventTypeOf(eventKey string) string {
	if eventKey == "paket-hampers" {
		return string(model.EventTypeBundle)
	}
	return string(model.EventTypeDiskon)
}

// orderCode builds a human-readable order number that stays stable across runs.
func orderCode(key string) string {
	digest := id("order-code", key)
	return "NSTR-" + uuid.UUID(digest).String()[:8]
}
