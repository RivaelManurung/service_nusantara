package seed

import (
	"fmt"
	"os"
	"strings"
)

// Seeded images live under one deterministic prefix, so a URL can be computed
// from a key rather than looked up in a manifest that could drift from the data.
const assetPrefix = "nusantara/seed"

// Asset folders, one per kind of image the fixture needs.
const (
	FolderProducts = "products"
	FolderTypes    = "types"
	FolderShops    = "shops"
	FolderBanners  = "banners"
	FolderEvents   = "events"
	FolderAvatars  = "avatars"
)

// ImageTarget is one image the fixture expects to exist.
//
// The generator renders these and the uploader stores them at PublicID(), so
// the three steps agree on names without passing a file between them.
type ImageTarget struct {
	Folder   string `json:"folder"`
	Key      string `json:"key"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	// Accent groups images that should share a colour, e.g. a product category.
	Accent string `json:"accent"`
}

// PublicID is the storage handle for this image.
func (t ImageTarget) PublicID() string {
	return fmt.Sprintf("%s/%s/%s", assetPrefix, t.Folder, t.Key)
}

// assetPublicID builds a handle without needing a full target.
func assetPublicID(folder, key string) string {
	return fmt.Sprintf("%s/%s/%s", assetPrefix, folder, key)
}

// assetURL is the delivery URL for a seeded image.
//
// Cloudinary serves an asset without the version segment, so the URL is
// derivable from the cloud name and the handle alone -- no manifest, and the
// seeder can fill in the public_id column at the same time.
//
// When CLOUDINARY_CLOUD_NAME is absent the fixture falls back to the unresolvable
// .test host it always used, so seeding still works without image credentials.
func assetURL(folder, key string) string {
	cloud := strings.TrimSpace(os.Getenv("CLOUDINARY_CLOUD_NAME"))
	if cloud == "" {
		return fmt.Sprintf("https://cdn.nusantara.test/%s/%s.jpg", folder, key)
	}
	return fmt.Sprintf("https://res.cloudinary.com/%s/image/upload/%s.png",
		cloud, assetPublicID(folder, key))
}

// ImageTargets lists every image the fixture references.
//
// It is exported so the generator and the uploader can work from the same list
// the seeder does; adding a product here automatically adds its image.
func ImageTargets() []ImageTarget {
	var targets []ImageTarget

	typeNames := make(map[string]string, len(seedTypeProducts))
	for _, t := range seedTypeProducts {
		typeNames[t.Key] = t.Name
		targets = append(targets, ImageTarget{
			Folder: FolderTypes, Key: t.Key,
			Title: t.Name, Subtitle: "Kategori", Accent: t.Key,
		})
	}

	for _, p := range seedProducts {
		targets = append(targets, ImageTarget{
			Folder: FolderProducts, Key: p.Key,
			Title: p.Name, Subtitle: p.Code, Accent: p.Type,
		})
		// The gallery's second image, referenced as "<key>-2".
		targets = append(targets, ImageTarget{
			Folder: FolderProducts, Key: p.Key + "-2",
			Title: p.Name, Subtitle: typeNames[p.Type], Accent: p.Type,
		})
	}

	for _, s := range seedShops {
		targets = append(targets,
			ImageTarget{Folder: FolderShops, Key: s.Key, Title: s.Name, Subtitle: "Galeri", Accent: s.Key},
			ImageTarget{Folder: FolderShops, Key: s.Key + "-cover", Title: s.Name, Subtitle: "Outlet", Accent: s.Key},
		)
	}

	for _, b := range seedBanners {
		targets = append(targets, ImageTarget{
			Folder: FolderBanners, Key: b.Key,
			Title: b.Name, Subtitle: "Promo", Accent: b.Key,
		})
	}

	for _, key := range []string{"pekan-kopi", "hampers", "batik"} {
		targets = append(targets, ImageTarget{
			Folder: FolderEvents, Key: key, Title: eventTitles[key], Subtitle: "Event", Accent: key,
		})
	}

	for _, u := range seedUsers {
		targets = append(targets, ImageTarget{
			Folder: FolderAvatars, Key: u.Key,
			Title: u.Name, Subtitle: u.Role, Accent: u.Role,
		})
	}

	return targets
}

// eventTitles names the three seeded campaigns, kept beside the keys the
// promo stage uses so the two cannot drift apart.
var eventTitles = map[string]string{
	"pekan-kopi": "Pekan Kopi Nusantara",
	"hampers":    "Paket Hampers Lebaran",
	"batik":      "Hari Batik Nasional",
}
