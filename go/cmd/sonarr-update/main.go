// Command sonarr-update applies a downloaded Sonarr update package to the
// running install. It replaces the C# NzbDrone.Update process.
//
// See ../../CONVERSION.md for the rules every change must follow.
package main

import (
	"flag"
	"fmt"
	"os"

	"sonarr.tv/sonarr/internal/buildinfo"
)

func main() {
	showVersion := flag.Bool("version", false, "print build info and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(buildinfo.String())
		return
	}

	fmt.Fprintln(os.Stderr, "sonarr-update: not yet implemented (port pending in Phase 4/Update)")
	os.Exit(2)
}
