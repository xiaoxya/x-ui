// xui-porthop synchronizes an isolated nftables table from enabled panel inbounds.
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alireza0/x-ui/config"
	"github.com/alireza0/x-ui/porthop"
	_ "github.com/mattn/go-sqlite3"
)

func main() {
	dbPath := flag.String("db", config.GetDBPath(), "panel SQLite database")
	apply := flag.Bool("apply", false, "apply rules with nft (requires CAP_NET_ADMIN)")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected argument")
		os.Exit(2)
	}
	plan, err := load(*dbPath)
	if err != nil {
		fatal(err)
	}
	if !*apply {
		fmt.Print(plan)
		return
	}
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(plan)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err = cmd.Run(); err != nil {
		fatal(err)
	}
}
func load(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err = os.Stat(abs); err != nil {
		return "", err
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return "", err
	}
	defer db.Close()
	rows, err := db.Query("SELECT id, protocol, port, listen, stream_settings FROM inbounds WHERE enable = 1")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var inbounds []porthop.Inbound
	for rows.Next() {
		var in porthop.Inbound
		var listen, stream sql.NullString
		if err = rows.Scan(&in.ID, &in.Protocol, &in.Port, &listen, &stream); err != nil {
			return "", err
		}
		in.Listen = listen.String
		in.StreamSettings = stream.String
		if in.StreamSettings == "" {
			in.StreamSettings = "{}"
		}
		inbounds = append(inbounds, in)
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	return porthop.Build(inbounds)
}
func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
