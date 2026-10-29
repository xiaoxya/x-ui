package sub

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// mihomoConfig returns JSON (valid YAML) using the same strict share-link parser.
func mihomoConfig(links []string) ([]byte, error) {
	proxies := []map[string]any{}
	names := []string{}
	for index, group := range links {
		for _, link := range strings.Split(group, "\n") {
			if strings.TrimSpace(link) == "" {
				continue
			}
			name := fmt.Sprintf("node-%d", len(names)+1)
			outbound, xhttp, err := mihomoLinkOutbound(link, name)
			if err != nil {
				return nil, fmt.Errorf("node %d: %w", index+1, err)
			}
			proxy, err := mihomoProxy(outbound)
			if err != nil {
				return nil, fmt.Errorf("node %d: %w", index+1, err)
			}
			if xhttp != nil {
				proxy["network"] = "xhttp"
				proxy["xhttp-opts"] = xhttp
			}
			proxies = append(proxies, proxy)
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no supported nodes")
	}
	group := append(append([]string{}, names...), "DIRECT")
	cfg := map[string]any{"mixed-port": 2080, "allow-lan": false, "mode": "rule", "proxies": proxies, "proxy-groups": []map[string]any{{"name": "select", "type": "select", "proxies": group}}, "rules": []string{"MATCH,select"}}
	return json.MarshalIndent(cfg, "", "  ")
}

// mihomoLinkOutbound handles XHTTP, which is supported by Mihomo but not sing-box.
func mihomoLinkOutbound(link, name string) (map[string]any, map[string]any, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, nil, err
	}
	if u.Scheme == "vmess" {
		out, err := singboxOutbound(link, name)
		return out, nil, err
	}
	q := u.Query()
	// SpiderX affects Reality's decoy crawler after a failed handshake, not the
	// authenticated tunnel; Mihomo has no equivalent setting.
	if q.Get("security") == "reality" {
		q.Del("spx")
	}
	if enc := q.Get("encryption"); enc == "none" {
		q.Del("encryption")
	}
	var xhttp map[string]any
	if q.Get("type") == "xhttp" {
		if u.Scheme != "vless" {
			return nil, nil, fmt.Errorf("XHTTP requires VLESS")
		}
		if extra := q.Get("extra"); extra != "" && extra != "{}" {
			return nil, nil, fmt.Errorf("unsupported XHTTP extra parameters")
		}
		mode := q.Get("mode")
		if mode != "" && mode != "auto" && mode != "stream-one" && mode != "stream-up" && mode != "packet-up" {
			return nil, nil, fmt.Errorf("unsupported XHTTP mode")
		}
		xhttp = map[string]any{}
		for _, key := range []string{"path", "host", "mode"} {
			if value := q.Get(key); value != "" {
				xhttp[key] = value
			}
			q.Del(key)
		}
		q.Del("extra")
		q.Set("type", "tcp")
	}
	u.RawQuery = q.Encode()
	out, err := singboxOutbound(u.String(), name)
	return out, xhttp, err
}

func mihomoProxy(out map[string]any) (map[string]any, error) {
	typ, _ := out["type"].(string)
	p := map[string]any{"name": out["tag"], "server": out["server"], "port": out["server_port"], "udp": true}
	switch typ {
	case "vless":
		p["type"] = "vless"
		p["uuid"] = out["uuid"]
		if v := out["flow"]; v != nil {
			p["flow"] = v
		}
	case "vmess":
		p["type"] = "vmess"
		p["uuid"] = out["uuid"]
		p["alterId"] = out["alter_id"]
		p["cipher"] = out["security"]
	case "trojan", "hysteria2":
		p["type"] = typ
		p["password"] = out["password"]
	case "shadowsocks":
		p["type"] = "ss"
		p["cipher"] = out["method"]
		p["password"] = out["password"]
	default:
		return nil, fmt.Errorf("unsupported protocol %s", typ)
	}
	if transport, ok := out["transport"].(map[string]any); ok {
		switch transport["type"] {
		case "ws":
			opts := map[string]any{}
			if v := transport["path"]; v != nil {
				opts["path"] = v
			}
			if v := transport["headers"]; v != nil {
				opts["headers"] = v
			}
			p["network"] = "ws"
			p["ws-opts"] = opts
		case "grpc":
			p["network"] = "grpc"
			p["grpc-opts"] = map[string]any{"grpc-service-name": transport["service_name"]}
		case "httpupgrade":
			opts := map[string]any{"v2ray-http-upgrade": true}
			if v := transport["path"]; v != nil {
				opts["path"] = v
			}
			if v := transport["host"]; v != nil {
				opts["headers"] = map[string]any{"Host": v}
			}
			p["network"] = "ws"
			p["ws-opts"] = opts
		default:
			return nil, fmt.Errorf("unsupported transport")
		}
	}
	if ports, ok := out["server_ports"].([]string); ok {
		values := make([]string, len(ports))
		for i, port := range ports {
			values[i] = strings.ReplaceAll(port, ":", "-")
		}
		p["ports"] = strings.Join(values, ",")
		if interval, ok := out["hop_interval"].(string); ok {
			minimum, err := time.ParseDuration(interval)
			if err != nil {
				return nil, err
			}
			value := strconv.FormatInt(int64(minimum/time.Second), 10)
			if max, ok := out["hop_interval_max"].(string); ok {
				d, err := time.ParseDuration(max)
				if err != nil {
					return nil, err
				}
				value += "-" + strconv.FormatInt(int64(d/time.Second), 10)
			}
			p["hop-interval"] = value
		}
	}
	if tls, ok := out["tls"].(map[string]any); ok {
		if typ == "shadowsocks" {
			return nil, fmt.Errorf("unsupported Shadowsocks TLS")
		}
		if typ != "hysteria2" {
			p["tls"] = true
		}
		if v := tls["server_name"]; v != nil {
			if typ == "trojan" || typ == "hysteria2" {
				p["sni"] = v
			} else {
				p["servername"] = v
			}
		}
		if v := tls["insecure"]; v != nil {
			p["skip-cert-verify"] = v
		}
		if v := tls["alpn"]; v != nil {
			p["alpn"] = v
		}
		if utls, ok := tls["utls"].(map[string]any); ok {
			p["client-fingerprint"] = utls["fingerprint"]
		}
		if reality, ok := tls["reality"].(map[string]any); ok {
			p["reality-opts"] = map[string]any{"public-key": reality["public_key"], "short-id": reality["short_id"]}
		}
	}
	return p, nil
}
