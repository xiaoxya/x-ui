package porthop

import (
	"os/exec"
	"strings"
	"testing"
)

func hop(id, port int, listen, ports string) Inbound {
	return Inbound{ID: id, Protocol: "hysteria", Port: port, Listen: listen, StreamSettings: `{"finalmask":{"quicParams":{"udpHop":{"ports":"` + ports + `"}}}}`}
}
func TestBuildRejectsUnsafePlans(t *testing.T) {
	for _, items := range [][]Inbound{
		{hop(1, 443, "", "100-200"), hop(2, 444, "", "200-300")},
		{hop(1, 443, "", "100-200"), {ID: 2, Protocol: "vless", Port: 150}},
		{hop(1, 443, "127.0.0.1", "100-200")},
		{hop(1, 443, "", "1-65536")},
		{hop(1, 443, "", "1;flush ruleset")},
	} {
		if _, err := Build(items); err == nil {
			t.Errorf("accepted unsafe plan: %+v", items)
		}
	}
}
func TestBuildFamilyAndAtomicReplacement(t *testing.T) {
	plan, err := Build([]Inbound{hop(1, 443, "0.0.0.0", "100-110,120"), hop(2, 444, "::", "300-310")})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"destroy table inet xui_porthop", "meta nfproto ipv4 udp dport 100-110 redirect to :443", "meta nfproto ipv6 udp dport 300-310 redirect to :444"} {
		if !strings.Contains(plan, want) {
			t.Fatalf("missing %q in %s", want, plan)
		}
	}
}
func TestNamespacePortForwarding(t *testing.T) {
	if _, err := exec.LookPath("unshare"); err != nil {
		t.Skip("unshare unavailable")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("nft unavailable")
	}
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("ip unavailable")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	plan, err := Build([]Inbound{hop(1, 31003, "", "31001-31002")})
	if err != nil {
		t.Fatal(err)
	}
	script := `import os,socket,subprocess,threading
plan=os.environ['TEST_NFT_PLAN']
subprocess.run(['ip','link','set','lo','up'],check=True)
child=subprocess.Popen(['unshare','--net','sleep','30'])
try:
 subprocess.run(['ip','link','add','veth-server','type','veth','peer','name','veth-client'],check=True)
 subprocess.run(['ip','link','set','veth-client','netns',str(child.pid)],check=True)
 subprocess.run(['ip','address','add','10.97.0.1/24','dev','veth-server'],check=True)
 subprocess.run(['ip','-6','address','add','fd97::1/64','dev','veth-server','nodad'],check=True)
 subprocess.run(['ip','link','set','veth-server','up'],check=True)
 def remote(*args): subprocess.run(['nsenter','-t',str(child.pid),'-n',*args],check=True)
 remote('ip','link','set','lo','up')
 remote('ip','address','add','10.97.0.2/24','dev','veth-client')
 remote('ip','-6','address','add','fd97::2/64','dev','veth-client','nodad')
 remote('ip','link','set','veth-client','up')
 subprocess.run(['nft','add','table','inet','unrelated'],check=True)
 def apply(): subprocess.run(['nft','-f','-'],input=plan,text=True,check=True)
 apply()
 apply()
 subprocess.run(['nft','list','table','inet','unrelated'],check=True,stdout=subprocess.DEVNULL)
 s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM)
 s.bind(('0.0.0.0',31003));s.settimeout(5)
 s6=socket.socket(socket.AF_INET6,socket.SOCK_DGRAM);s6.setsockopt(socket.IPPROTO_IPV6,socket.IPV6_V6ONLY,1);s6.bind(('::',31003));s6.settimeout(5)
 def echo():
  for _ in range(2):
   data,peer=s.recvfrom(1024);s.sendto(data,peer)
 t=threading.Thread(target=echo,daemon=True);t.start()
 def echo6():
  for _ in range(2):
   data,peer=s6.recvfrom(1024);s6.sendto(data,peer)
 t6=threading.Thread(target=echo6,daemon=True);t6.start()
 client="import socket; c=socket.socket(socket.AF_INET,socket.SOCK_DGRAM); c.settimeout(3); [(c.sendto(b'test',('10.97.0.1',p)), c.recvfrom(1024)) for p in (31001,31002)]"
 remote('python3','-c',client)
 client6="import socket; c=socket.socket(socket.AF_INET6,socket.SOCK_DGRAM); c.settimeout(3); [(c.sendto(b'test',('fd97::1',p)), c.recvfrom(1024)) for p in (31001,31002)]"
 remote('python3','-c',client6)
 t.join(5);t6.join(5)
 assert not t.is_alive() and not t6.is_alive()
finally:
 child.terminate();child.wait()
`
	cmd := exec.Command("unshare", "--user", "--map-root-user", "--net", "python3", "-c", script)
	cmd.Env = append(cmd.Environ(), "TEST_NFT_PLAN="+plan)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "Operation not permitted") {
			t.Skipf("user namespace not permitted: %s", out)
		}
		t.Fatalf("namespace forwarding failed: %v\n%s", err, out)
	}
}
