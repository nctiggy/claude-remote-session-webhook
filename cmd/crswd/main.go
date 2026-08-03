// Command crswd is the claude-remote-session-webhook daemon.
//
// Configuration is environment-only (CRSW_*), so no flags are defined here yet;
// flag.Parse still runs so -h reports usage rather than an unknown-flag error.
package main

import "flag"

func main() {
	flag.Parse()
}
