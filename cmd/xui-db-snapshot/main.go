package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/alireza0/x-ui/deployment"
)

func main() {
	source := flag.String("db", "/etc/x-ui/x-ui.db", "source SQLite database")
	root := flag.String("backup-dir", "/var/backups/x-ui", "directory for private snapshots")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected arguments")
		os.Exit(2)
	}
	path, err := deployment.SnapshotDatabase(*source, *root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(path)
}
