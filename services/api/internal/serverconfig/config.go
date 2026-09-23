// Package serverconfig validates runtime settings shared by the Control Plane
// entrypoint and its unit tests.
package serverconfig

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Required returns a non-blank environment value.
func Required(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

// String returns an optional environment value or its fallback.
func String(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

// Duration parses a strictly positive optional duration.
func Duration(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return duration, nil
}

// Megabytes parses a positive size limit. Invalid values retain the safe
// fallback, matching the existing deployment behavior.
func Megabytes(name string, fallback int64) int64 {
	value, err := strconv.ParseInt(os.Getenv(name), 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value * 1024 * 1024
}

// Bool parses an optional boolean environment value.
func Bool(name, value string, fallback bool) (bool, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", name)
	}
	return parsed, nil
}

// EmailDomains parses an optional comma-separated list of email domains. An
// empty value leaves registration unrestricted. Entries are normalized to
// lowercase and duplicates are removed while retaining their first order.
func EmailDomains(name string) ([]string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return nil, nil
	}

	domains := make([]string, 0, len(strings.Split(value, ",")))
	seen := make(map[string]struct{})
	for _, entry := range strings.Split(value, ",") {
		domain := strings.ToLower(strings.TrimSpace(entry))
		if !validEmailDomain(domain) {
			return nil, fmt.Errorf("%s contains invalid email domain %q", name, entry)
		}
		if _, ok := seen[domain]; ok {
			continue
		}
		seen[domain] = struct{}{}
		domains = append(domains, domain)
	}
	return domains, nil
}

func validEmailDomain(domain string) bool {
	if len(domain) == 0 || len(domain) > 253 || strings.ContainsAny(domain, "@/\\") {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-') {
				return false
			}
		}
	}
	return true
}

// IsLoopbackAddress reports whether address contains an explicit loopback
// host and port. Wildcard listeners are intentionally rejected.
func IsLoopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
