package sub

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/alireza0/x-ui/database/model"
	"github.com/alireza0/x-ui/web/service"
)

// quickTunnelInbound builds a client-facing copy. cloudflared's quick tunnel
// forwards HTTP to IPv4 loopback, so only plain WebSocket listeners reachable
// there can safely be advertised through it.
func quickTunnelInbound(in *model.Inbound, statuses []service.TunnelStatus) *model.Inbound {
	if !in.Enable || (in.Protocol != model.VLESS && in.Protocol != model.VMess && in.Protocol != model.Trojan) {
		return nil
	}
	if in.Listen != "" && in.Listen != "0.0.0.0" && in.Listen != "127.0.0.1" && in.Listen != "localhost" {
		return nil
	}
	var domain string
	for _, status := range statuses {
		if status.Mode != "quick" || !status.Running || status.Port != in.Port {
			continue
		}
		u, err := url.Parse(status.Domain)
		if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			continue
		}
		name := strings.ToLower(u.Hostname())
		label, ok := strings.CutSuffix(name, ".trycloudflare.com")
		if !ok || label == "" || label == "api" || strings.Contains(label, ".") {
			continue
		}
		domain = name
		break
	}
	if domain == "" {
		return nil
	}
	var stream map[string]any
	if json.Unmarshal([]byte(in.StreamSettings), &stream) != nil || stream == nil || stream["network"] != "ws" {
		return nil
	}
	if security, _ := stream["security"].(string); security != "" && security != "none" {
		return nil
	}
	ws, ok := stream["wsSettings"].(map[string]any)
	if !ok {
		ws = map[string]any{}
	}
	if host, ok := ws["host"].(string); ok && host != "" && !strings.EqualFold(host, domain) {
		return nil
	}
	if headers, ok := ws["headers"].(map[string]any); ok {
		for k, v := range headers {
			if strings.EqualFold(k, "host") {
				host, ok := v.(string)
				if !ok || (host != "" && !strings.EqualFold(host, domain)) {
					return nil
				}
				delete(headers, k)
			}
		}
	}
	ws["host"] = domain
	stream["wsSettings"] = ws
	stream["externalProxy"] = []any{map[string]any{"dest": domain, "port": 443, "forceTls": "tls", "sni": domain, "remark": "Cloudflare"}}
	encoded, err := json.Marshal(stream)
	if err != nil {
		return nil
	}
	copy := *in
	copy.StreamSettings = string(encoded)
	return &copy
}
