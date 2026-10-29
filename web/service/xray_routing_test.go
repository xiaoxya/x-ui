package service

import (
	"encoding/json"
	"testing"

	"github.com/alireza0/x-ui/util/json_util"
)

// Xray-core refuses a routing script next to rules or a domain strategy, so
// both stay out of the config while a script is set.
func TestMergeRoutingRulesLeavesRulesOutForScript(t *testing.T) {
	s := &XrayService{}
	merged, err := s.mergeRoutingRules(json_util.RawMessage(
		`{"script":"route.lua","domainStrategy":"IPIfNonMatch","rules":[],"balancers":[{"tag":"b","selector":["x"]}]}`))
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	var routing map[string]any
	if err := json.Unmarshal(merged, &routing); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if _, ok := routing["rules"]; ok {
		t.Fatalf("rules written next to a script: %s", merged)
	}
	if _, ok := routing["domainStrategy"]; ok {
		t.Fatalf("domainStrategy written next to a script: %s", merged)
	}
	if routing["script"] != "route.lua" || routing["balancers"] == nil {
		t.Fatalf("script or balancers lost: %s", merged)
	}
}
