package role

// Catalogue of every permission the application recognises.
//
// This list is the source of truth. The rows in the `permissions` table are
// seeded from it by migration 0003, and catalog_test.go fails the build if the
// two ever disagree -- so a code that middleware checks can never be missing
// from the table, and a row in the table can never name a permission nothing
// enforces.
//
// Adding a permission therefore means: append it here, and add the same tuple
// to a NEW migration. Never edit an applied one; the runner rejects it by
// checksum.

// Groups are the headings the admin UI renders the matrix under.
const (
	GroupCatalogue = "Katalog"
	GroupShop      = "Toko"
	GroupOrder     = "Pesanan"
	GroupPromotion = "Promosi"
	GroupReport    = "Laporan"
	GroupAccount   = "Pengguna"
	GroupSystem    = "Sistem"
)

// Codes, referenced from Go rather than typed as literals at each call site.
const (
	PermTypeProductRead  = "type_product.read"
	PermTypeProductWrite = "type_product.write"
	PermProductRead      = "product.read"
	PermProductWrite     = "product.write"

	PermShopRead         = "shop.read"
	PermShopWrite        = "shop.write"
	PermShopProductRead  = "shop_product.read"
	PermShopProductWrite = "shop_product.write"
	PermCashierRead      = "cashier.read"
	PermCashierWrite     = "cashier.write"

	PermOrderRead  = "order.read"
	PermOrderWrite = "order.write"

	PermBannerRead   = "banner.read"
	PermBannerWrite  = "banner.write"
	PermEventRead    = "event.read"
	PermEventWrite   = "event.write"
	PermVoucherRead  = "voucher.read"
	PermVoucherWrite = "voucher.write"

	PermReportTransactionRead = "report_transaction.read"
	PermReportFinancialRead   = "report_financial.read"

	PermUserRead  = "user.read"
	PermUserWrite = "user.write"

	PermRoleRead          = "role.read"
	PermRoleWrite         = "role.write"
	PermNotificationRead  = "notification.read"
	PermNotificationWrite = "notification.write"
)

// Definition is one catalogue entry, before it is given a database id.
type Definition struct {
	Code  string
	Label string
	Group string
}

// catalogue is unexported so no caller can append to the backing array; use
// Catalog() for a copy.
var catalogue = []Definition{
	{PermTypeProductRead, "Lihat tipe produk", GroupCatalogue},
	{PermTypeProductWrite, "Kelola tipe produk", GroupCatalogue},
	{PermProductRead, "Lihat produk", GroupCatalogue},
	{PermProductWrite, "Kelola produk", GroupCatalogue},

	{PermShopRead, "Lihat toko", GroupShop},
	{PermShopWrite, "Kelola toko", GroupShop},
	{PermShopProductRead, "Lihat produk toko", GroupShop},
	{PermShopProductWrite, "Kelola produk toko", GroupShop},
	{PermCashierRead, "Lihat kasir", GroupShop},
	{PermCashierWrite, "Kelola kasir", GroupShop},

	{PermOrderRead, "Lihat pesanan", GroupOrder},
	{PermOrderWrite, "Kelola pesanan", GroupOrder},

	{PermBannerRead, "Lihat banner", GroupPromotion},
	{PermBannerWrite, "Kelola banner", GroupPromotion},
	{PermEventRead, "Lihat event", GroupPromotion},
	{PermEventWrite, "Kelola event", GroupPromotion},
	{PermVoucherRead, "Lihat voucher", GroupPromotion},
	{PermVoucherWrite, "Kelola voucher", GroupPromotion},

	{PermReportTransactionRead, "Lihat laporan transaksi", GroupReport},
	{PermReportFinancialRead, "Lihat laporan keuangan", GroupReport},

	{PermUserRead, "Lihat pengguna", GroupAccount},
	{PermUserWrite, "Kelola pengguna", GroupAccount},

	{PermRoleRead, "Lihat role dan akses", GroupSystem},
	{PermRoleWrite, "Kelola role dan akses", GroupSystem},
	{PermNotificationRead, "Lihat notifikasi", GroupSystem},
	{PermNotificationWrite, "Kirim notifikasi", GroupSystem},
}

// Catalog returns a copy of every known permission, in display order.
func Catalog() []Definition {
	out := make([]Definition, len(catalogue))
	copy(out, catalogue)
	return out
}

// KnownCodes reports the catalogue as a set, for validating a submitted list.
func KnownCodes() map[string]struct{} {
	set := make(map[string]struct{}, len(catalogue))
	for _, def := range catalogue {
		set[def.Code] = struct{}{}
	}
	return set
}

// AllCodes returns every code in catalogue order.
func AllCodes() []string {
	codes := make([]string, 0, len(catalogue))
	for _, def := range catalogue {
		codes = append(codes, def.Code)
	}
	return codes
}
