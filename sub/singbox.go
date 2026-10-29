package sub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// singboxConfig converts supported share links to a standalone local client.
// Unsupported links fail the request instead of being silently dropped.
func singboxConfig(links []string) ([]byte, error) {
	outbounds := []map[string]any{{"type": "direct", "tag": "direct"}}
	tags := []string{"direct"}
	for index, group := range links {
		for _, link := range strings.Split(group, "\n") {
			if strings.TrimSpace(link) == "" {
				continue
			}
			tag := fmt.Sprintf("node-%d", len(tags))
			outbound, err := singboxOutbound(link, tag)
			if err != nil {
				return nil, fmt.Errorf("node %d: %w", index+1, err)
			}
			if ports, ok := outbound["server_ports"].([]string); ok {
				if interval, ok := outbound["hop_interval"].(string); ok {
					duration, err := time.ParseDuration(interval)
					if err != nil || duration < 5*time.Second {
						return nil, fmt.Errorf("node %d: sing-box hop interval must be at least 5 seconds", index+1)
					}
				}
				normalized := make([]string, len(ports))
				for i, port := range ports {
					if !strings.Contains(port, ":") {
						normalized[i] = port + ":" + port
					} else {
						normalized[i] = port
					}
				}
				outbound["server_ports"] = normalized
				delete(outbound, "server_port")
			}
			outbounds = append(outbounds, outbound)
			tags = append(tags, tag)
		}
	}
	if len(tags) == 1 {
		return nil, fmt.Errorf("no supported nodes")
	}
	outbounds = append([]map[string]any{{"type": "selector", "tag": "select", "outbounds": tags, "default": tags[1]}}, outbounds...)
	config := map[string]any{
		"log":       map[string]any{"level": "warn"},
		"dns":       map[string]any{"servers": []map[string]any{{"type": "local", "tag": "local-dns"}}},
		"inbounds":  []map[string]any{{"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 2080}},
		"outbounds": outbounds,
		"route":     map[string]any{"final": "select", "default_domain_resolver": "local-dns"},
	}
	return json.MarshalIndent(config, "", "  ")
}

func singboxOutbound(link, tag string) (map[string]any, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "vmess" {
		return singboxVMess(link, tag)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 || u.Hostname() == "" || u.User == nil {
		return nil, fmt.Errorf("invalid node address")
	}
	q := u.Query()
	// "none" is the ordinary unencrypted VLESS payload mode and is implicit
	// in sing-box; other encryption modes cannot be represented here.
	if u.Scheme == "vless" && q.Get("encryption") == "none" {
		q.Del("encryption")
	}
	hopping := map[string]any{}
	if ports := q.Get("mport"); ports != "" {
		if u.Scheme != "hysteria2" {
			return nil, fmt.Errorf("port hopping requires Hysteria2")
		}
		ranges, err := parseHopPorts(ports)
		if err != nil {
			return nil, err
		}
		hopping["server_ports"] = ranges
		q.Del("mport")
	}
	if interval := q.Get("udphopInterval"); interval != "" {
		if hopping["server_ports"] == nil {
			return nil, fmt.Errorf("hop interval requires port ranges")
		}
		min, max, err := parseHopInterval(interval)
		if err != nil {
			return nil, err
		}
		hopping["hop_interval"] = min
		if max != "" {
			hopping["hop_interval_max"] = max
		}
		q.Del("udphopInterval")
	}
	allowed := map[string]bool{"type": true, "security": true, "sni": true, "path": true, "host": true, "flow": true, "fp": true, "pbk": true, "sid": true, "alpn": true, "allowInsecure": true, "insecure": true, "serviceName": true, "authority": true}
	for key := range q {
		if !allowed[key] && q.Get(key) != "" {
			return nil, fmt.Errorf("unsupported parameter %s", key)
		}
	}
	if q.Get("authority") != "" {
		return nil, fmt.Errorf("unsupported gRPC authority")
	}
	out := map[string]any{"type": u.Scheme, "tag": tag, "server": u.Hostname(), "server_port": port}
	switch u.Scheme {
	case "vless":
		out["uuid"] = u.User.Username()
		if flow := q.Get("flow"); flow != "" {
			out["flow"] = flow
		}
	case "trojan", "hysteria2":
		out["password"] = u.User.Username()
	case "ss":
		out["type"] = "shadowsocks"
		raw, err := decodeBase64(u.User.Username())
		if err != nil {
			return nil, fmt.Errorf("invalid shadowsocks credentials")
		}
		parts := strings.SplitN(string(raw), ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid shadowsocks credentials")
		}
		out["method"], out["password"] = parts[0], parts[1]
	default:
		return nil, fmt.Errorf("unsupported protocol %s", u.Scheme)
	}
	if u.User.Username() == "" {
		return nil, fmt.Errorf("missing credential")
	}
	network := q.Get("type")
	if network == "" {
		network = "tcp"
	}
	if u.Scheme == "hysteria2" || u.Scheme == "ss" {
		if network != "tcp" {
			return nil, fmt.Errorf("unsupported transport %s", network)
		}
	} else {
		switch network {
		case "tcp", "raw":
		case "ws":
			transport := map[string]any{"type": "ws"}
			if path := q.Get("path"); path != "" {
				transport["path"] = path
			}
			if host := q.Get("host"); host != "" {
				transport["headers"] = map[string]string{"Host": host}
			}
			out["transport"] = transport
		case "grpc":
			out["transport"] = map[string]any{"type": "grpc", "service_name": q.Get("serviceName")}
		case "httpupgrade":
			transport := map[string]any{"type": "httpupgrade"}
			if path := q.Get("path"); path != "" {
				transport["path"] = path
			}
			if host := q.Get("host"); host != "" {
				transport["host"] = host
			}
			out["transport"] = transport
		default:
			return nil, fmt.Errorf("unsupported transport %s", network)
		}
	}
	security := q.Get("security")
	if u.Scheme == "hysteria2" && security == "" {
		security = "tls"
	}
	if security != "" && security != "none" {
		if security != "tls" && security != "reality" {
			return nil, fmt.Errorf("unsupported security %s", security)
		}
		tls := map[string]any{"enabled": true}
		if name := q.Get("sni"); name != "" {
			tls["server_name"] = name
		}
		if q.Get("allowInsecure") == "1" || q.Get("insecure") == "1" {
			tls["insecure"] = true
		}
		if alpn := q.Get("alpn"); alpn != "" {
			tls["alpn"] = strings.Split(alpn, ",")
		}
		if security == "reality" {
			if q.Get("pbk") == "" {
				return nil, fmt.Errorf("missing reality public key")
			}
			fp := q.Get("fp")
			if fp == "" {
				fp = "chrome"
			}
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
			tls["reality"] = map[string]any{"enabled": true, "public_key": q.Get("pbk"), "short_id": q.Get("sid")}
		} else if fp := q.Get("fp"); fp != "" {
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
		}
		out["tls"] = tls
	}
	for key, value := range hopping {
		out[key] = value
	}
	if u.Scheme == "hysteria2" && out["tls"] == nil {
		return nil, fmt.Errorf("hysteria2 requires TLS")
	}
	return out, nil
}
func decodeBase64(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, e := enc.DecodeString(s); e == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("invalid base64")
}
func singboxVMess(link, tag string) (map[string]any, error) {
	data, err := decodeBase64(strings.TrimPrefix(link, "vmess://"))
	if err != nil {
		return nil, err
	}
	var v struct {
		Add  string `json:"add"`
		Port any    `json:"port"`
		ID   string `json:"id"`
		Aid  any    `json:"aid"`
		Net  string `json:"net"`
		Type string `json:"type"`
		Host string `json:"host"`
		Path string `json:"path"`
		TLS  string `json:"tls"`
		SNI  string `json:"sni"`
	}
	if err = json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(fmt.Sprint(v.Port))
	if err != nil || port < 1 || port > 65535 || v.Add == "" || v.ID == "" {
		return nil, fmt.Errorf("invalid VMess")
	}
	aid := 0
	if v.Aid != nil {
		aid, err = strconv.Atoi(fmt.Sprint(v.Aid))
		if err != nil {
			return nil, err
		}
	}
	out := map[string]any{"type": "vmess", "tag": tag, "server": v.Add, "server_port": port, "uuid": v.ID, "security": "auto", "alter_id": aid}
	switch v.Net {
	case "", "tcp", "raw":
		if v.Type != "" && v.Type != "none" {
			return nil, fmt.Errorf("unsupported TCP header")
		}
	case "ws":
		transport := map[string]any{"type": "ws"}
		if v.Path != "" {
			transport["path"] = v.Path
		}
		if v.Host != "" {
			transport["headers"] = map[string]string{"Host": v.Host}
		}
		out["transport"] = transport
	default:
		return nil, fmt.Errorf("unsupported VMess transport %s", v.Net)
	}
	switch v.TLS {
	case "", "none":
	case "tls":
		tls := map[string]any{"enabled": true}
		if v.SNI != "" {
			tls["server_name"] = v.SNI
		}
		out["tls"] = tls
	default:
		return nil, fmt.Errorf("unsupported VMess security %s", v.TLS)
	}
	return out, nil
}

func parseHopPorts(value string) ([]string, error) {
	parts := strings.Split(value, ",")
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty port range")
	}
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		normalized := strings.ReplaceAll(part, "-", ":")
		bounds := strings.Split(normalized, ":")
		if len(bounds) < 1 || len(bounds) > 2 {
			return nil, fmt.Errorf("invalid port range")
		}
		first, err := strconv.Atoi(bounds[0])
		if err != nil || first < 1 || first > 65535 {
			return nil, fmt.Errorf("invalid port range")
		}
		if len(bounds) == 1 {
			result = append(result, strconv.Itoa(first))
			continue
		}
		last, err := strconv.Atoi(bounds[1])
		if err != nil || last < first || last > 65535 {
			return nil, fmt.Errorf("invalid port range")
		}
		result = append(result, fmt.Sprintf("%d:%d", first, last))
	}
	return result, nil
}
func parseHopInterval(value string) (string, string, error) {
	bounds := strings.Split(strings.TrimSpace(value), "-")
	if len(bounds) > 2 || len(bounds) == 0 {
		return "", "", fmt.Errorf("invalid hop interval")
	}
	parse := func(s string) (int, error) {
		v, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || v < 1 || v > 86400 {
			return 0, fmt.Errorf("invalid hop interval")
		}
		return v, nil
	}
	min, err := parse(bounds[0])
	if err != nil {
		return "", "", err
	}
	minValue := (time.Duration(min) * time.Second).String()
	if len(bounds) == 1 {
		return minValue, "", nil
	}
	max, err := parse(bounds[1])
	if err != nil || max < min {
		return "", "", fmt.Errorf("invalid hop interval")
	}
	return minValue, (time.Duration(max) * time.Second).String(), nil
}
