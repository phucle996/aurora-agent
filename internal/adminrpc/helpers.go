package adminrpc

import (
	"fmt"
	"net"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
)

func readStructString(req *structpb.Struct, key string) string {
	if req == nil {
		return ""
	}
	field, ok := req.GetFields()[key]
	if !ok || field == nil {
		return ""
	}
	return strings.TrimSpace(field.GetStringValue())
}

func readStructBool(req *structpb.Struct, key string, fallback bool) bool {
	if req == nil {
		return fallback
	}
	field, ok := req.GetFields()[key]
	if !ok || field == nil {
		return fallback
	}
	switch v := field.GetKind().(type) {
	case *structpb.Value_BoolValue:
		return v.BoolValue
	case *structpb.Value_NumberValue:
		return v.NumberValue > 0
	case *structpb.Value_StringValue:
		switch strings.ToLower(strings.TrimSpace(v.StringValue)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		default:
			return fallback
		}
	default:
		return fallback
	}
}

func readStructNumber(req *structpb.Struct, key string, fallback float64) float64 {
	if req == nil {
		return fallback
	}
	field, ok := req.GetFields()[key]
	if !ok || field == nil {
		return fallback
	}
	n := field.GetNumberValue()
	if n <= 0 {
		return fallback
	}
	return n
}

func readStructSecondsAsDuration(req *structpb.Struct, key string, fallback time.Duration) time.Duration {
	v := readStructNumber(req, key, 0)
	if v <= 0 {
		return fallback
	}
	return time.Duration(v) * time.Second
}

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
