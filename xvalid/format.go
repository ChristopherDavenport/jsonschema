package xvalid

import (
	"net/mail"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// CheckFormat reports whether value satisfies the named format. Unknown formats
// are treated as valid, matching the spec rule that unrecognized formats are
// annotations that never fail validation. The known return reports whether the
// format was recognized (useful for diagnostics).
func CheckFormat(format, value string) (valid, known bool) {
	fn, ok := formatCheckers[format]
	if !ok {
		return true, false
	}
	return fn(value), true
}

var formatCheckers = map[string]func(string) bool{
	"date-time":             IsDateTime,
	"date":                  IsDate,
	"time":                  IsTime,
	"duration":              IsDuration,
	"email":                 IsEmail,
	"idn-email":             IsEmail, // permissive superset for now
	"hostname":              IsHostname,
	"idn-hostname":          IsHostname, // permissive superset for now
	"ipv4":                  IsIPv4,
	"ipv6":                  IsIPv6,
	"uri":                   IsURI,
	"uri-reference":         IsURIReference,
	"iri":                   IsURI,          // permissive: treated as uri
	"iri-reference":         IsURIReference, // permissive: treated as uri-reference
	"uuid":                  IsUUID,
	"json-pointer":          IsJSONPointer,
	"relative-json-pointer": IsRelativeJSONPointer,
	"regex":                 IsRegex,
	"uri-template":          func(string) bool { return true }, // accept for now
}

// IsDateTime reports whether s is an RFC 3339 date-time.
func IsDateTime(s string) bool {
	_, err := time.Parse(time.RFC3339, s)
	return err == nil
}

// IsDate reports whether s is an RFC 3339 full-date (YYYY-MM-DD).
func IsDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// IsTime reports whether s is an RFC 3339 full-time.
func IsTime(s string) bool {
	for _, layout := range []string{"15:04:05Z07:00", "15:04:05.999999999Z07:00"} {
		if _, err := time.Parse(layout, s); err == nil {
			return true
		}
	}
	return false
}

var durationRe = regexp.MustCompile(`^P(?:\d+W|(?:\d+Y)?(?:\d+M)?(?:\d+D)?(?:T(?:\d+H)?(?:\d+M)?(?:\d+S)?)?)$`)

// IsDuration reports whether s is an ISO 8601 / RFC 3339 duration. It rejects
// the empty forms "P" and "PT" that the coarse grammar would otherwise admit.
func IsDuration(s string) bool {
	if s == "P" || s == "PT" {
		return false
	}
	return durationRe.MatchString(s)
}

// IsEmail reports whether s is a bare RFC 5321 mailbox (no display name).
func IsEmail(s string) bool {
	addr, err := mail.ParseAddress(s)
	return err == nil && addr.Name == "" && addr.Address == s
}

var hostnameRe = regexp.MustCompile(`^(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)

// IsHostname reports whether s is a valid RFC 1123 hostname.
func IsHostname(s string) bool {
	return len(s) <= 253 && hostnameRe.MatchString(s)
}

// IsIPv4 reports whether s is a dotted-quad IPv4 address.
func IsIPv4(s string) bool {
	addr, err := netip.ParseAddr(s)
	return err == nil && addr.Is4()
}

// IsIPv6 reports whether s is an IPv6 address.
func IsIPv6(s string) bool {
	addr, err := netip.ParseAddr(s)
	return err == nil && addr.Is6() && strings.Contains(s, ":")
}

// IsURI reports whether s is an absolute URI (has a scheme).
func IsURI(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.IsAbs()
}

// IsURIReference reports whether s is a URI reference (absolute or relative).
func IsURIReference(s string) bool {
	_, err := url.Parse(s)
	return err == nil
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsUUID reports whether s is a canonical RFC 4122 UUID.
func IsUUID(s string) bool { return uuidRe.MatchString(s) }

// IsJSONPointer reports whether s is an RFC 6901 JSON Pointer.
func IsJSONPointer(s string) bool {
	if s == "" {
		return true
	}
	if s[0] != '/' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] == '~' {
			if i+1 >= len(s) || (s[i+1] != '0' && s[i+1] != '1') {
				return false
			}
			i++
		}
	}
	return true
}

// IsRelativeJSONPointer reports whether s is an RFC 6901 relative JSON pointer.
func IsRelativeJSONPointer(s string) bool {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 { // must start with a non-negative integer
		return false
	}
	if i > 1 && s[0] == '0' { // no leading zeros
		return false
	}
	if _, err := strconv.Atoi(s[:i]); err != nil {
		return false
	}
	rest := s[i:]
	if rest == "" {
		return false
	}
	if rest == "#" {
		return true
	}
	return IsJSONPointer(rest)
}

// IsRegex reports whether s compiles as a regular expression, adapting the
// ECMA-262 constructs RE2 spells differently (see CompilePattern). Constructs
// RE2 lacks entirely (lookaround, backreferences) are still rejected.
func IsRegex(s string) bool {
	_, err := CompilePattern(s)
	return err == nil
}
