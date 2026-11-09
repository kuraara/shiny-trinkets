// diskscan: scans a machine's disks and keeps snapshots for the Disk Space gadget.
//
//	diskscan scan [flags]     one scan, written to a file or stdout (ncdu JSON; .gz if the name ends so)
//	diskscan serve            run as the worker: scan on a schedule, keep snapshots, serve them over HTTP
//	diskscan version
//
// Read-only: it never changes, moves or deletes anything it scans.
package main

import (
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "scan":
		cmdScan(os.Args[2:])
	case "serve":
		if err := serve(configFromEnv()); err != nil {
			fmt.Fprintln(os.Stderr, "diskscan:", err)
			os.Exit(1)
		}
	case "version", "-v", "--version":
		fmt.Println("diskscan", version)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: diskscan scan [-root DIR] [-name /] [-o FILE[.gz]] [-keep 20] [-min-mb 100] [-exclude /a,/b]\n       diskscan serve   (settings from the environment; see README)\n       diskscan version")
	os.Exit(2)
}

func cmdScan(args []string) {
	fl := flag.NewFlagSet("scan", flag.ExitOnError)
	root := fl.String("root", "/", "directory to scan")
	name := fl.String("name", "/", "what to call the root in the snapshot")
	outPath := fl.String("o", "-", "output file (- for stdout); gzip-compressed when it ends in .gz")
	keep := fl.Int("keep", 20, "files listed per directory (the biggest)")
	minMB := fl.Int64("min-mb", 100, "files at least this many MB are always listed")
	exclude := fl.String("exclude", "", "comma-separated paths (as named in the snapshot) to skip")
	allowFS := fl.String("allow-fs", "", "extra filesystem types to scan, e.g. fuse,nfs")
	fl.Parse(args)

	var out io.Writer = os.Stdout
	if *outPath != "-" {
		f, err := os.Create(*outPath)
		if err != nil {
			fail(err)
		}
		defer f.Close()
		out = f
		if strings.HasSuffix(*outPath, ".gz") {
			gz := gzip.NewWriter(f)
			defer gz.Close()
			out = gz
		}
	}
	st, err := Scan(Options{
		Root: *root, RootName: *name, KeepFiles: *keep, MinFile: *minMB << 20,
		Exclude: splitList(*exclude), AllowFS: splitList(*allowFS),
	}, out, nil)
	if err != nil {
		fail(err)
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	fmt.Fprintln(os.Stderr, string(b))
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

func envStr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "diskscan:", err)
	os.Exit(1)
}
