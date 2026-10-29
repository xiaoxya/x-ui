// Package porthop builds an isolated nftables NAT table for Hysteria2 hopping.
package porthop

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type Inbound struct {
	ID             int
	Protocol       string
	Port           int
	Listen         string
	StreamSettings string
}
type portRange struct {
	start, end, target, id int
	family                 string
}

// Build produces an atomic nft transaction. It owns only inet xui_porthop.
func Build(inbounds []Inbound) (string, error) {
	var ranges []portRange
	for _, in := range inbounds {
		if in.Protocol != "hysteria" {
			continue
		}
		var stream struct {
			FinalMask struct {
				QuicParams struct {
					UDPHop struct {
						Ports string `json:"ports"`
					} `json:"udpHop"`
				} `json:"quicParams"`
			} `json:"finalmask"`
		}
		if err := json.Unmarshal([]byte(in.StreamSettings), &stream); err != nil {
			return "", fmt.Errorf("inbound %d: invalid stream settings: %w", in.ID, err)
		}
		ports := strings.TrimSpace(stream.FinalMask.QuicParams.UDPHop.Ports)
		if ports == "" {
			continue
		}
		if in.Port < 1 || in.Port > 65535 {
			return "", fmt.Errorf("inbound %d: invalid target port", in.ID)
		}
		family := ""
		switch in.Listen {
		case "":
		case "0.0.0.0":
			family = "meta nfproto ipv4 "
		case "::", "::0":
			family = "meta nfproto ipv6 "
		default:
			return "", fmt.Errorf("inbound %d: hopping requires a wildcard listen address", in.ID)
		}
		for _, part := range strings.Split(ports, ",") {
			bounds := strings.Split(strings.ReplaceAll(strings.TrimSpace(part), ":", "-"), "-")
			if len(bounds) < 1 || len(bounds) > 2 {
				return "", fmt.Errorf("inbound %d: invalid port range", in.ID)
			}
			first, err := strconv.Atoi(bounds[0])
			if err != nil || first < 1 || first > 65535 {
				return "", fmt.Errorf("inbound %d: invalid port range", in.ID)
			}
			last := first
			if len(bounds) == 2 {
				last, err = strconv.Atoi(bounds[1])
				if err != nil || last < first || last > 65535 {
					return "", fmt.Errorf("inbound %d: invalid port range", in.ID)
				}
			}
			ranges = append(ranges, portRange{first, last, in.Port, in.ID, family})
		}
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
	for i := 1; i < len(ranges); i++ {
		if ranges[i].start <= ranges[i-1].end {
			return "", fmt.Errorf("overlapping ranges for inbounds %d and %d", ranges[i-1].id, ranges[i].id)
		}
	}
	// Avoid redirecting an existing inbound's main port to a different service.
	for _, r := range ranges {
		for _, in := range inbounds {
			if in.Port >= r.start && in.Port <= r.end && in.Port != r.target {
				return "", fmt.Errorf("range for inbound %d includes inbound %d main port", r.id, in.ID)
			}
		}
	}
	var out strings.Builder
	out.WriteString("destroy table inet xui_porthop\nadd table inet xui_porthop\nadd chain inet xui_porthop prerouting { type nat hook prerouting priority dstnat; policy accept; }\n")
	for _, r := range ranges {
		ports := strconv.Itoa(r.start)
		if r.end != r.start {
			ports += fmt.Sprintf("-%d", r.end)
		}
		fmt.Fprintf(&out, "add rule inet xui_porthop prerouting %sudp dport %s redirect to :%d\n", r.family, ports, r.target)
	}
	return out.String(), nil
}
