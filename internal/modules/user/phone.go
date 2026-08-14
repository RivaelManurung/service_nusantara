package user

import (
	"strings"

	"service_nusantara/internal/httpx"
)

// phoneMinDigits and phoneMaxDigits bound an E.164 number (ITU-T caps the
// national part at 15 digits including the country code).
const (
	phoneMinDigits = 8
	phoneMaxDigits = 15
)

// normalizePhone converts the shapes users actually type into one canonical
// E.164 string, so "081234567890", "+6281234567890" and "62 812-3456-7890" all
// resolve to the same account.
//
// The previous helper prefixed "+62" onto anything starting with "0" without
// validating the rest, so "0abc" became the stored phone number "+62abc".
func normalizePhone(raw, defaultCountryCode string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", httpx.BadRequest("phone number is required")
	}

	hadPlus := strings.HasPrefix(trimmed, "+")

	var digits strings.Builder
	for _, r := range trimmed {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case r == '+' || r == ' ' || r == '-' || r == '(' || r == ')' || r == '.':
			// Separators people type are ignored rather than rejected.
		default:
			return "", httpx.BadRequest("phone number may only contain digits and separators")
		}
	}

	number := digits.String()
	switch {
	case hadPlus:
		// Already international.
	case strings.HasPrefix(number, "0"):
		// A national number such as 0812... becomes 62812...
		number = defaultCountryCode + strings.TrimLeft(number, "0")
	case strings.HasPrefix(number, defaultCountryCode):
		// Already carries the country code, just without the plus.
	default:
		number = defaultCountryCode + number
	}

	if len(number) < phoneMinDigits || len(number) > phoneMaxDigits {
		return "", httpx.BadRequest("phone number length is not valid")
	}
	if strings.HasPrefix(number, "0") {
		return "", httpx.BadRequest("phone number is not valid")
	}

	return "+" + number, nil
}
