// Command pesubsystem checks that a Windows executable has the expected PE
// subsystem (2 = GUI, 3 = console), so release builds of Goti.exe never open
// a console window.
//
//	go run ./packaging/cmd/pesubsystem -want 2 dist/Goti.exe
package main

import (
	"debug/pe"
	"flag"
	"fmt"
	"os"
)

func main() {
	want := flag.Int("want", 2, "expected subsystem (2 GUI, 3 console)")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: pesubsystem [-want N] FILE.exe")
		os.Exit(2)
	}
	got, err := subsystem(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "pesubsystem:", err)
		os.Exit(1)
	}
	if got != uint16(*want) {
		fmt.Fprintf(os.Stderr, "pesubsystem: %s: subsystem %d, want %d\n", flag.Arg(0), got, *want)
		os.Exit(1)
	}
	fmt.Printf("%s: subsystem %d ok\n", flag.Arg(0), got)
}

func subsystem(path string) (uint16, error) {
	f, err := pe.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	switch h := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		return h.Subsystem, nil
	case *pe.OptionalHeader32:
		return h.Subsystem, nil
	}
	return 0, fmt.Errorf("%s: no optional header", path)
}
