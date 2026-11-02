package xray

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alireza0/x-ui/logger"
)

func TestNormalizeFreedomStrategy(t *testing.T) {
	for _, tc := range []struct{ name, target, settings, stream, want string }{
		{"legacy", "", `{"domainStrategy":"UseIP","redirect":"x"}`, `{"sockopt":{"mark":7}}`, "UseIP"},
		{"target wins", "ForceIPv4", `{"domainStrategy":"UseIP"}`, `{"sockopt":{"domainStrategy":"UseIPv6"}}`, "ForceIPv4"},
		{"asis keeps socket", "AsIs", `{"domainStrategy":"AsIs"}`, `{"sockopt":{"domainStrategy":"UseIPv6"}}`, "UseIPv6"},
		{"inner target", "", `{"targetStrategy":"UseIPv4","domainStrategy":"UseIP"}`, `{}`, "UseIPv4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &OutboundConfig{Protocol: "freedom", TargetStrategy: tc.target, Settings: []byte(tc.settings), StreamSettings: []byte(tc.stream)}
			if err := NormalizeFreedomStrategy(c); err != nil {
				t.Fatal(err)
			}
			var settings map[string]any
			var stream struct {
				Sockopt map[string]any `json:"sockopt"`
			}
			if err := json.Unmarshal(c.Settings, &settings); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(c.StreamSettings, &stream); err != nil {
				t.Fatal(err)
			}
			if _, ok := settings["domainStrategy"]; ok {
				t.Fatal("legacy strategy retained")
			}
			if _, ok := settings["targetStrategy"]; ok {
				t.Fatal("legacy target retained")
			}
			if c.TargetStrategy != "" || stream.Sockopt["domainStrategy"] != tc.want {
				t.Fatalf("unexpected config: %+v %s", c, c.StreamSettings)
			}
			if tc.name == "legacy" && (stream.Sockopt["mark"] != float64(7) || settings["redirect"] != "x") {
				t.Fatal("other settings lost")
			}
		})
	}
}

func TestLogWriterStartupIsInfoButWarningsRemain(t *testing.T) {
	w := NewLogWriter()
	_, _ = w.Write([]byte("2026/11/02 14:19:53.000000 [Warning] core: Xray 26.10.31 started\n2026/11/02 14:19:53.000000 [Warning] infra/conf: actual warning\n"))
	logs := strings.Join(logger.GetLogs(100, "DEBUG"), "\n")
	if !strings.Contains(logs, "INFO - XRAY: core: Xray 26.10.31 started") || !strings.Contains(logs, "WARNING - XRAY: infra/conf: actual warning") {
		t.Fatal(logs)
	}
}
