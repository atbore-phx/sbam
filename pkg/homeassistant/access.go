package homeassistant

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// SupervisorURL is the Core API root reachable from an add-on with
// homeassistant_api: true.
const SupervisorURL = "http://supervisor/core/api"

// supervisorTokenEnv is read only through the injected getenv, never via viper.
const supervisorTokenEnv = "SUPERVISOR_TOKEN"

// entityIDPattern follows ADR-0001 / S-014.
var entityIDPattern = regexp.MustCompile(`^[a-z0-9_]+\.[a-z0-9_]+$`)

// ValidateEntityID reports whether id is a safe Home Assistant entity ID.
func ValidateEntityID(id string) error {
	if !entityIDPattern.MatchString(id) {
		return fmt.Errorf("homeassistant: invalid entity id %q: must match %s", id, entityIDPattern.String())
	}
	return nil
}

// ResolveAccess decides how sbam reaches Home Assistant (ADR-0001, AC-21).
//
//  1. If haURL is set it wins and haToken is required.
//  2. Otherwise, if SUPERVISOR_TOKEN is set, the Supervisor proxy is used.
//  3. Otherwise an error is returned.
//
// getenv is normally os.Getenv; it is injected for tests.
func ResolveAccess(haURL, haToken string, getenv func(string) string) (Config, error) {
	haURL = strings.TrimSpace(haURL)
	haToken = strings.TrimSpace(haToken)

	if haURL != "" {
		if haToken == "" {
			return Config{}, errors.New("homeassistant: ha_token is required when ha_url is set")
		}
		base, err := normalizeBaseURL(haURL)
		if err != nil {
			return Config{}, err
		}
		return Config{BaseURL: base, Token: haToken, Source: SourceURL}, nil
	}

	if getenv != nil {
		if tok := strings.TrimSpace(getenv(supervisorTokenEnv)); tok != "" {
			return Config{BaseURL: SupervisorURL, Token: tok, Source: SourceSupervisor}, nil
		}
	}

	return Config{}, errors.New("homeassistant: no access configured: set ha_url and ha_token, or run as an add-on with homeassistant_api: true")
}

// normalizeBaseURL validates a user-supplied ha_url (S-014) and returns it
// with a path ending in "/api".
func normalizeBaseURL(raw string) (string, error) {
	u, err := parseBaseURL(raw)
	if err != nil {
		return "", err
	}
	p := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(p, "/api") {
		p += "/api"
	}
	u.Path = p
	u.RawPath = ""
	return u.String(), nil
}

// parseBaseURL enforces http/https, a host, and no userinfo, query or fragment.
// Error messages never echo the raw URL, since it could contain userinfo.
func parseBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("homeassistant: ha_url is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("homeassistant: ha_url scheme must be http or https")
	}
	if u.User != nil {
		return nil, errors.New("homeassistant: ha_url must not contain userinfo")
	}
	if u.Host == "" || u.Hostname() == "" {
		return nil, errors.New("homeassistant: ha_url must contain a host")
	}
	if u.RawQuery != "" || u.ForceQuery {
		return nil, errors.New("homeassistant: ha_url must not contain a query")
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return nil, errors.New("homeassistant: ha_url must not contain a fragment")
	}
	return u, nil
}

// PlainHTTPToPublicHost reports whether the config sends the token over plain
// http to a host that is neither loopback nor private. The caller should log a
// Warn in that case (ADR-0001); this package does not log.
func (c Config) PlainHTTPToPublicHost() bool {
	if c.Source == SourceSupervisor {
		return false
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// A hostname: treat single-label names (e.g. "homeassistant") and
		// .local mDNS names as local, anything else as potentially public.
		h := strings.ToLower(host)
		return strings.Contains(h, ".") && !strings.HasSuffix(h, ".local") && !strings.HasSuffix(h, ".lan")
	}
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}
