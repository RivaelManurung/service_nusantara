package storage_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"service_nusantara/internal/platform/storage"
)

func TestPublicIDFromURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
		ok   bool
	}{
		{
			// The shape actually stored by this account, confirmed against the
			// live API before this function was written.
			name: "versioned url with folders",
			url:  "https://res.cloudinary.com/dwdwa65i4/image/upload/v1786610746/RinelPortfolio/Blog/Featured/hdt6q08nzt15h5p2mv4b.webp",
			want: "RinelPortfolio/Blog/Featured/hdt6q08nzt15h5p2mv4b",
			ok:   true,
		},
		{
			name: "no version segment",
			url:  "https://res.cloudinary.com/demo/image/upload/nusantara/products/abc123.jpg",
			want: "nusantara/products/abc123",
			ok:   true,
		},
		{
			name: "with transformations",
			url:  "https://res.cloudinary.com/demo/image/upload/w_200,c_fill/f_auto/v123/nusantara/banners/xyz.png",
			want: "nusantara/banners/xyz",
			ok:   true,
		},
		{
			name: "no extension",
			url:  "https://res.cloudinary.com/demo/image/upload/v1/nusantara/shops/cover",
			want: "nusantara/shops/cover",
			ok:   true,
		},
		{
			// A folder legitimately containing a dot must survive intact.
			name: "dot inside a folder name",
			url:  "https://res.cloudinary.com/demo/image/upload/v1/nusantara/v1.2/logo.png",
			want: "nusantara/v1.2/logo",
			ok:   true,
		},
		{name: "not a cloudinary url", url: "https://example.com/image.png", want: "", ok: false},
		{name: "empty", url: "", want: "", ok: false},
		{name: "upload with nothing after it", url: "https://res.cloudinary.com/demo/image/upload/", want: "", ok: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := storage.PublicIDFromURL(tc.url)

			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}
