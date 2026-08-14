package seed

import (
	"fmt"
	"strings"
)

// The dataset below is the demo catalogue: an oleh-oleh (Indonesian souvenir)
// business with outlets in five cities. Values are realistic so that screens
// built against it look like the real product, and prices are in rupiah.

// Role names. These must match what the application expects: config's
// DEFAULT_SIGNUP_ROLE defaults to "customer".
const (
	roleSuperAdmin = "superadmin"
	roleAdmin      = "admin"
	roleCashier    = "cashier"
	roleCustomer   = "customer"
)

var roleNames = []string{roleSuperAdmin, roleAdmin, roleCashier, roleCustomer}

// seedUser describes one account in the fixture.
type seedUser struct {
	Key      string
	Name     string
	Username string
	Email    string
	Phone    string
	Password string
	Role     string
	Gender   string
	// Providers are the sign-in methods linked to this account, exercising the
	// Google / Apple / phone / password flows end to end.
	Providers []string
}

// demoPassword is shared by every seeded account.
//
// It is long enough to satisfy the registration rules and obviously fake, so a
// seeded database cannot be mistaken for one holding real credentials.
const demoPassword = "NusantaraDemo123!"

var seedUsers = []seedUser{
	{
		Key: "superadmin", Name: "Super Admin Nusantara", Username: "superadmin",
		Email: "superadmin@nusantara.test", Phone: "+6281100000001",
		Password: demoPassword, Role: roleSuperAdmin, Gender: "male",
		Providers: []string{providerPassword},
	},
	{
		Key: "admin", Name: "Admin Katalog", Username: "admin.katalog",
		Email: "admin@nusantara.test", Phone: "+6281100000002",
		Password: demoPassword, Role: roleAdmin, Gender: "female",
		Providers: []string{providerPassword},
	},
	{
		Key: "cashier.malioboro", Name: "Rina Kasir Malioboro", Username: "kasir.malioboro",
		Email: "kasir.malioboro@nusantara.test", Phone: "+6281100000011",
		Password: demoPassword, Role: roleCashier, Gender: "female",
		Providers: []string{providerPassword},
	},
	{
		Key: "cashier.braga", Name: "Dimas Kasir Braga", Username: "kasir.braga",
		Email: "kasir.braga@nusantara.test", Phone: "+6281100000012",
		Password: demoPassword, Role: roleCashier, Gender: "male",
		Providers: []string{providerPassword},
	},
	{
		Key: "cashier.kuta", Name: "Wayan Kasir Kuta", Username: "kasir.kuta",
		Email: "kasir.kuta@nusantara.test", Phone: "+6281100000013",
		Password: demoPassword, Role: roleCashier, Gender: "male",
		Providers: []string{providerPassword},
	},
	// Customers, one per sign-in method, so every login path has a fixture.
	{
		Key: "customer.budi", Name: "Budi Santoso", Username: "budi.santoso",
		Email: "budi@nusantara.test", Phone: "+6281200000001",
		Password: demoPassword, Role: roleCustomer, Gender: "male",
		Providers: []string{providerPassword, providerGoogle},
	},
	{
		Key: "customer.siti", Name: "Siti Aminah", Username: "siti.aminah",
		Email: "siti@nusantara.test", Phone: "+6281200000002",
		Password: demoPassword, Role: roleCustomer, Gender: "female",
		Providers: []string{providerPassword, providerApple},
	},
	{
		// Phone-only: no email, no username, no password. This is the account
		// shape the OTP flow creates.
		Key: "customer.dewi", Name: "Dewi Lestari", Phone: "+6281200000003",
		Role: roleCustomer, Gender: "female",
		Providers: []string{providerPhone},
	},
	{
		// Google-only: no password, so /auth/login must reject it.
		Key: "customer.agus", Name: "Agus Pratama",
		Email: "agus@nusantara.test", Role: roleCustomer, Gender: "male",
		Providers: []string{providerGoogle},
	},
}

// Provider names, duplicated here rather than imported from the user module so
// the seeder does not depend on a feature package.
const (
	providerPassword = "password"
	providerPhone    = "phone"
	providerGoogle   = "google"
	providerApple    = "apple"
)

// seedTypeProduct is a product category.
type seedTypeProduct struct {
	Key   string
	Name  string
	Image string
}

var seedTypeProducts = []seedTypeProduct{
	{"keripik", "Keripik & Kerupuk", "https://cdn.nusantara.test/types/keripik.jpg"},
	{"dodol", "Dodol & Manisan", "https://cdn.nusantara.test/types/dodol.jpg"},
	{"kue", "Kue Kering", "https://cdn.nusantara.test/types/kue-kering.jpg"},
	{"bakpia", "Bakpia & Pia", "https://cdn.nusantara.test/types/bakpia.jpg"},
	{"kopi", "Kopi & Teh", "https://cdn.nusantara.test/types/kopi.jpg"},
	{"sambal", "Sambal & Bumbu", "https://cdn.nusantara.test/types/sambal.jpg"},
	{"kain", "Batik & Kain", "https://cdn.nusantara.test/types/batik.jpg"},
	{"kerajinan", "Kerajinan Tangan", "https://cdn.nusantara.test/types/kerajinan.jpg"},
}

// seedProduct is one catalogue item.
type seedProduct struct {
	Key         string
	Name        string
	Code        string
	Type        string
	Price       int
	Unit        string
	Description string
}

var seedProducts = []seedProduct{
	{"bakpia-25-keju", "Bakpia Pathok 25 Keju", "BPK-KEJU-20", "bakpia", 48000, "box", "Bakpia isi keju premium, 20 buah per kotak. Oleh-oleh khas Yogyakarta."},
	{"bakpia-25-kacang", "Bakpia Pathok 25 Kacang Hijau", "BPK-KCH-20", "bakpia", 42000, "box", "Bakpia klasik isi kacang hijau, dipanggang setiap pagi."},
	{"bakpia-kukus", "Bakpia Kukus Tugu Coklat", "BPK-KUK-CKL", "bakpia", 55000, "box", "Bakpia kukus lembut dengan isian coklat lumer."},
	{"gudeg-kaleng", "Gudeg Kaleng Bu Tjitro", "GDG-KLG-350", "sambal", 62000, "kaleng", "Gudeg siap saji dalam kaleng 350 gram, tahan 12 bulan."},
	{"keripik-tempe", "Keripik Tempe Malang", "KRP-TMP-250", "keripik", 28000, "pack", "Keripik tempe tipis renyah, kemasan 250 gram."},
	{"keripik-pisang", "Keripik Pisang Lampung Cokelat", "KRP-PSG-200", "keripik", 32000, "pack", "Keripik pisang salut cokelat asli Lampung."},
	{"keripik-singkong", "Keripik Singkong Balado", "KRP-SGK-200", "keripik", 24000, "pack", "Keripik singkong pedas manis balado."},
	{"kerupuk-udang", "Kerupuk Udang Sidoarjo", "KRP-UDG-500", "keripik", 45000, "pack", "Kerupuk udang kandungan udang 30 persen."},
	{"dodol-garut", "Dodol Garut Picnic Wijen", "DDL-GRT-500", "dodol", 38000, "pack", "Dodol Garut legendaris dengan taburan wijen."},
	{"wingko-babat", "Wingko Babat Cap Kereta Api", "WGK-BBT-10", "dodol", 35000, "box", "Wingko kelapa panggang, isi 10 buah."},
	{"lapis-legit", "Lapis Legit Premium", "KUE-LPS-PRM", "kue", 185000, "box", "Lapis legit 18 lapis dengan mentega premium."},
	{"kue-lapis-surabaya", "Lapis Surabaya Klasik", "KUE-LPS-SBY", "kue", 145000, "box", "Lapis Surabaya tiga lapis dengan selai blueberry."},
	{"nastar-keju", "Nastar Keju Premium", "KUE-NST-KJU", "kue", 95000, "toples", "Nastar isi nanas asli dengan topping keju edam."},
	{"kastengel", "Kastengel Edam", "KUE-KTG-EDM", "kue", 105000, "toples", "Kastengel renyah dengan keju edam, 500 gram."},
	{"kopi-gayo", "Kopi Arabika Gayo 200g", "KOP-GAY-200", "kopi", 78000, "pack", "Biji kopi arabika Gayo, medium roast, dari Aceh Tengah."},
	{"kopi-toraja", "Kopi Arabika Toraja 200g", "KOP-TRJ-200", "kopi", 82000, "pack", "Arabika Toraja dengan karakter earthy dan low acidity."},
	{"kopi-luwak", "Kopi Luwak Bali 100g", "KOP-LWK-100", "kopi", 245000, "pack", "Kopi luwak asli Bali, kemasan 100 gram."},
	{"teh-poci", "Teh Poci Melati", "TEH-PCI-MLT", "kopi", 22000, "pack", "Teh melati khas Tegal, kemasan 100 gram."},
	{"sambal-roa", "Sambal Roa Manado", "SMB-ROA-200", "sambal", 55000, "botol", "Sambal ikan roa asap khas Manado."},
	{"sambal-bawang", "Sambal Bawang Bu Rudy", "SMB-BWG-200", "sambal", 48000, "botol", "Sambal bawang pedas dengan bawang goreng."},
	{"bumbu-rendang", "Bumbu Rendang Padang", "BMB-RDG-150", "sambal", 32000, "pack", "Bumbu rendang instan racikan Padang."},
	{"batik-tulis", "Batik Tulis Pekalongan", "BTK-TLS-PKL", "kain", 450000, "lembar", "Batik tulis motif Pekalongan, katun primissima."},
	{"batik-cap", "Batik Cap Solo", "BTK-CAP-SLO", "kain", 185000, "lembar", "Batik cap motif parang, bahan katun."},
	{"tenun-ikat", "Tenun Ikat Sumba", "TNN-IKT-SMB", "kain", 675000, "lembar", "Tenun ikat Sumba pewarna alam, tenun tangan."},
	{"wayang-kulit", "Wayang Kulit Mini Arjuna", "KRJ-WYG-ARJ", "kerajinan", 165000, "pcs", "Wayang kulit mini ukir tangan, tinggi 40 cm."},
	{"miniatur-becak", "Miniatur Becak Kayu", "KRJ-BCK-MIN", "kerajinan", 85000, "pcs", "Miniatur becak dari kayu jati, panjang 20 cm."},
	{"tas-rotan", "Tas Rotan Anyaman Bali", "KRJ-TAS-RTN", "kerajinan", 195000, "pcs", "Tas rotan anyaman tangan pengrajin Bali."},
	{"gantungan-kunci", "Gantungan Kunci Ukir", "KRJ-GTK-UKR", "kerajinan", 15000, "pcs", "Gantungan kunci kayu ukir motif nusantara."},
}

// seedShop is one outlet.
type seedShop struct {
	Key         string
	Name        string
	Description string
	Address     string
	Lat         float64
	Lng         float64
	CashierKey  string
}

var seedShops = []seedShop{
	{
		Key: "malioboro", Name: "Nusantara Oleh-Oleh Malioboro",
		Description: "Outlet utama di jantung Malioboro, buka 08.00 sampai 22.00.",
		Address:     "Jl. Malioboro No. 52, Sosromenduran, Gedong Tengen, Yogyakarta",
		Lat:         -7.792680, Lng: 110.365780, CashierKey: "cashier.malioboro",
	},
	{
		Key: "braga", Name: "Nusantara Oleh-Oleh Braga",
		Description: "Outlet Bandung dengan koleksi keripik dan kopi Jawa Barat.",
		Address:     "Jl. Braga No. 88, Sumur Bandung, Kota Bandung",
		Lat:         -6.917460, Lng: 107.609810, CashierKey: "cashier.braga",
	},
	{
		Key: "kuta", Name: "Nusantara Oleh-Oleh Kuta",
		Description: "Outlet Bali, fokus kerajinan tangan dan kopi luwak.",
		Address:     "Jl. Pantai Kuta No. 21, Kuta, Badung, Bali",
		Lat:         -8.717930, Lng: 115.168820, CashierKey: "cashier.kuta",
	},
	{
		Key: "tunjungan", Name: "Nusantara Oleh-Oleh Tunjungan",
		Description: "Outlet Surabaya dengan sambal dan kerupuk Jawa Timur.",
		Address:     "Jl. Tunjungan No. 15, Genteng, Surabaya",
		Lat:         -7.259700, Lng: 112.737600,
	},
	{
		Key: "kesawan", Name: "Nusantara Oleh-Oleh Kesawan",
		Description: "Outlet Medan dengan bolu dan kopi Sumatera.",
		Address:     "Jl. Ahmad Yani No. 7, Kesawan, Medan Barat, Medan",
		Lat:         3.586700, Lng: 98.677800,
	},
}

// seedBanner is a promotional slot on the home screen.
type seedBanner struct {
	Key         string
	Name        string
	Description string
	Photo       string
}

var seedBanners = []seedBanner{
	{"lebaran", "Hampers Lebaran 2026", "Paket hampers kue kering dan bakpia, gratis kartu ucapan.", "https://cdn.nusantara.test/banners/lebaran.jpg"},
	{"gratis-ongkir", "Gratis Ongkir Se-Jawa", "Minimal belanja Rp150.000, berlaku untuk seluruh outlet.", "https://cdn.nusantara.test/banners/ongkir.jpg"},
	{"kopi-nusantara", "Pekan Kopi Nusantara", "Diskon 20 persen untuk seluruh kopi single origin.", "https://cdn.nusantara.test/banners/kopi.jpg"},
	{"batik-day", "Hari Batik Nasional", "Koleksi batik tulis dan cap pilihan pengrajin.", "https://cdn.nusantara.test/banners/batik.jpg"},
}

// seedVoucher is a discount coupon.
type seedVoucher struct {
	Key             string
	Code            string
	Description     string
	DiscountType    string
	DiscountAmount  int
	DiscountPercent int
	MinimumSpend    int
	PointCost       int
	Quota           int
	// StartOffsetDays and EndOffsetDays are relative to the run, so a seeded
	// database always has vouchers that are currently valid.
	StartOffsetDays int
	EndOffsetDays   int
}

var seedVouchers = []seedVoucher{
	{"welcome", "NUSANTARA10", "Diskon 10 persen untuk pembelian pertama.", "percent", 0, 10, 100000, 0, 1000, -7, 60},
	{"ongkir", "GRATISONGKIR", "Potongan ongkir Rp15.000 se-Jawa.", "amount", 15000, 0, 150000, 0, 500, -3, 30},
	{"kopi", "KOPI20", "Diskon 20 persen khusus kategori kopi.", "percent", 0, 20, 75000, 0, 300, -1, 14},
	{"poin", "TUKARPOIN50", "Tukar 500 poin jadi potongan Rp50.000.", "amount", 50000, 0, 200000, 500, 200, -14, 90},
	{"lebaran", "LEBARAN25", "Diskon 25 persen paket hampers Lebaran.", "percent", 0, 25, 250000, 0, 150, -2, 45},
	{"kadaluarsa", "EXPIRED2025", "Voucher kedaluwarsa, untuk menguji penolakan.", "percent", 0, 15, 50000, 0, 100, -120, -30},
}

// DemoPassword exposes the shared fixture password so the command can print it.
func DemoPassword() string { return demoPassword }

// AccountSummary lists the seeded accounts and how each one signs in, which is
// the first thing anyone needs after running the seeder.
func AccountSummary() []string {
	lines := make([]string, 0, len(seedUsers))
	for _, u := range seedUsers {
		identifier := u.Email
		if identifier == "" {
			identifier = u.Phone
		}
		lines = append(lines, fmt.Sprintf("%-11s %-32s via %s",
			u.Role, identifier, strings.Join(u.Providers, ", ")))
	}
	return lines
}
