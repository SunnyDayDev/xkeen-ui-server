package server

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

type WebOrigin string

var webDNSLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func ParseWebOrigin(raw string) (WebOrigin, error) {
	invalid := errors.New("invalid --web-origin: expected an HTTPS origin without a path")
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || strings.ContainsAny(raw, "?#") || u.Path != "" {
		return "", invalid
	}
	host, port := strings.ToLower(u.Hostname()), u.Port()
	switch addr, err := netip.ParseAddr(host); {
	case err == nil && addr.Zone() == "":
		host = addr.String()
		if addr.Is6() {
			host = "[" + host + "]"
		}
	case err != nil && len(host) <= 253:
		for _, label := range strings.Split(host, ".") {
			if !webDNSLabel.MatchString(label) {
				return "", invalid
			}
		}
	default:
		return "", invalid
	}
	if port != "" {
		number, err := strconv.ParseUint(port, 10, 16)
		if err != nil || number == 0 || strconv.FormatUint(number, 10) != port {
			return "", invalid
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return "", invalid
	}
	if strings.HasPrefix(u.Host, "[") != strings.HasPrefix(host, "[") {
		return "", invalid
	}
	if port != "" && port != "443" {
		host = net.JoinHostPort(strings.Trim(host, "[]"), port)
	}
	return WebOrigin("https://" + host), nil
}

func (o WebOrigin) allows(r *http.Request, requireOrigin bool) bool {
	authority, err := ParseWebOrigin("https://" + r.Host)
	if r.TLS == nil || err != nil || authority != o {
		return false
	}
	headers := r.Header.Values("Origin")
	if len(headers) == 0 {
		return !requireOrigin
	}
	if len(headers) != 1 {
		return false
	}
	requestOrigin, err := ParseWebOrigin(headers[0])
	return err == nil && requestOrigin == o
}
