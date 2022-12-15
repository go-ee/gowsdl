// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
/*

Gowsdl generates Go code from a WSDL file.

This project is originally intended to generate Go clients for WS-* services.

Usage: gowsdl [options] myservice.wsdl
  -o string
        File where the generated code will be saved (default "myservice.go")
  -p string
        Package under which code will be generated (default "myservice")
  -v    Shows gowsdl version

Features

Supports only Document/Literal wrapped services, which are WS-I (http://ws-i.org/) compliant.

Attempts to generate idiomatic Go code as much as possible.

Supports WSDL 1.1, XML Schema 1.0, SOAP 1.1.

Resolves external XML Schemas

Supports providing WSDL HTTP URL as well as a local WSDL file.

Not supported

UDDI.

TODO

Add support for filters to allow the user to change the generated code.

If WSDL file is local, resolve external XML schemas locally too instead of failing due to not having a URL to download them from.

Resolve XSD element references.

Support for generating namespaces.

Make code generation agnostic so generating code to other programming languages is feasible through plugins.

*/

package main

import (
	"flag"
	"fmt"
	"github.com/hooklift/gowsdl"
	"log"
	"os"
	"strings"
)

// Version is initialized in compilation time by go build.
var Version string

// name is initialized in compilation time by go build.
var name string

var vers = flag.Bool("v", false, "Shows gowsdl version")
var filePrefix = flag.String("l", "myervice_", "File prefix, label")
var pkg = flag.String("p", "myservice", "Package under which code will be generated")
var dir = flag.String("d", "./", "Directory under which service package directory will be created")
var insecure = flag.Bool("i", false, "Skips TLS Verification")
var makePublic = flag.Bool("make-public", true, "Make the generated types public/exported")

func init() {
	log.SetFlags(0)
	log.SetOutput(os.Stdout)
	log.SetPrefix("🍀  ")
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] myservice.wsdl\n", os.Args[0])
		flag.PrintDefaults()
	}

	flag.Parse()

	// Show app version
	if *vers {
		log.Println(Version)
		os.Exit(0)
	}

	if len(os.Args) < 2 {
		flag.Usage()
		os.Exit(0)
	}

	if err := generate(); err != nil {
		log.Fatalln(err)
	}
}

func generate() (err error) {
	wsdlPath := os.Args[len(os.Args)-1]

	// load wsdl
	var wsdl *gowsdl.GoWSDL
	if wsdl, err = gowsdl.NewGoWSDL(
		wsdlPath, *filePrefix,
		strings.TrimSpace(*dir),
		strings.TrimSpace(*pkg),
		*insecure, *makePublic); err != nil {
		return
	}

	// generate code
	if err = wsdl.Generate(); err != nil {
		return
	}

	log.Println("Done 👍")
	return
}
