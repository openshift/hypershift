package primaryudn

import "strings"

func dnsZoneFromHostname(host string) string {
	if host == "" {
		return ""
	}
	if i := strings.IndexByte(host, '.'); i > 0 && i+1 < len(host) {
		return host[i+1:]
	}
	return host
}

func uniqueStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
