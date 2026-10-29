package sub

import (
	"fmt"
	"net/url"
	"path/filepath"
	"sync"
	"testing"

	"github.com/alireza0/x-ui/database"
	"github.com/alireza0/x-ui/database/model"
)

func TestSubscriptionHostsAreRequestScoped(t *testing.T) {
	if err := database.InitDB(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatal(err)
	}
	defer database.CloseDB()
	inbound := model.Inbound{Enable: true, Port: 443, Protocol: model.VLESS, Tag: "test", Settings: `{"clients":[{"id":"27848739-7e62-4138-9fd3-098a63964b6b","email":"alice","subId":"token","enable":true}],"decryption":"none"}`, StreamSettings: `{"network":"tcp","security":"none"}`}
	if err := database.GetDB().Create(&inbound).Error; err != nil {
		t.Fatal(err)
	}
	service := NewSubService(false, "-ieo")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			host := fmt.Sprintf("node-%d.example", i)
			links, _, err := service.GetSubs("token", host)
			if err != nil || len(links) != 1 {
				t.Errorf("links %v error %v", links, err)
				return
			}
			u, err := url.Parse(links[0])
			if err != nil || u.Hostname() != host {
				t.Errorf("host mismatch got %q expected %q err %v", links[0], host, err)
			}
		}(i)
	}
	wg.Wait()
}

func TestSubscriptionHost(t *testing.T) {
	for input, want := range map[string]string{"example.org:443": "example.org", "example.org": "example.org", "[2001:db8::1]:443": "2001:db8::1", "[2001:db8::1]": "2001:db8::1", "2001:db8::1": "2001:db8::1"} {
		if got := subscriptionHost(input); got != want {
			t.Errorf("%q => %q, want %q", input, got, want)
		}
	}
}

func TestShareURIEscapesCredentialsAndIPv6(t *testing.T) {
	for _, scheme := range []string{"vless", "trojan", "ss", "hysteria2"} {
		encoded := shareURI(scheme, "a:b@c/#", "2001:db8::5", 443)
		parsed, err := url.Parse(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.Hostname() != "2001:db8::5" || parsed.Port() != "443" || parsed.User.Username() != "a:b@c/#" {
			t.Fatalf("bad URI: %q", encoded)
		}
	}
}

func TestGeneratedLinksHandleIPv6AndSpecialPasswords(t *testing.T) {
	service := testService()
	service.address = "2001:db8::9"
	for _, tc := range []struct {
		protocol model.Protocol
		settings string
	}{
		{model.Trojan, `{"clients":[{"password":"pa:ss@/word","email":"e"}]}`},
		{model.Hysteria, `{"clients":[{"auth":"pa:ss@/word","email":"e"}]}`},
	} {
		link := service.getLink(inboundFor(tc.protocol, tc.settings, `{"network":"tcp","security":"tls"}`), "e")
		parsed, err := url.Parse(link)
		if err != nil {
			t.Fatalf("%s: %v", link, err)
		}
		if parsed.Hostname() != "2001:db8::9" || parsed.User.Username() != "pa:ss@/word" {
			t.Fatalf("bad %s link: %q", tc.protocol, link)
		}
	}
}
