package adminrpc

import (
	"fmt"
	"net"
	"strings"
)

func uniqueStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		v := strings.TrimSpace(item)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func uniqueIPs(items []net.IP) []net.IP {
	seen := make(map[string]struct{}, len(items))
	out := make([]net.IP, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		normalized := item.String()
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, item)
	}
	return out
}

func serverEndpointHost(endpoint string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(endpoint))
	if err != nil {
		return ""
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" || net.ParseIP(host) != nil {
		return ""
	}
	return host
}

func parseEndpointIP(endpoint string) net.IP {
	host, _, err := net.SplitHostPort(strings.TrimSpace(endpoint))
	if err != nil {
		return nil
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	return net.ParseIP(host)
}

func errHeartbeatClientNil() error {
	return fmt.Errorf("heartbeat client is nil")
}
