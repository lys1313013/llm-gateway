// Command pwhashcheck is a tiny helper used by the cross-language test.
//
// Usage:
//   pwhashcheck verify <hash> <password>
//   pwhashcheck gen <password>
//
// Exists solely so cross_test.py (and humans) can confirm that Go's
// pbkdf2 implementation interoperates with Werkzeug.
package main

import (
	"fmt"
	"os"

	"github.com/lys1313013/llm-gateway/backend/internal/auth"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: pwhashcheck <verify|gen> ...")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "verify":
		if len(os.Args) != 4 {
			fmt.Fprintln(os.Stderr, "usage: pwhashcheck verify <hash> <password>")
			os.Exit(2)
		}
		if auth.VerifyPassword(os.Args[3], os.Args[2]) {
			fmt.Println("true")
		} else {
			fmt.Println("false")
			os.Exit(1)
		}
	case "gen":
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: pwhashcheck gen <password>")
			os.Exit(2)
		}
		h, err := auth.HashPassword(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(h)
	default:
		fmt.Fprintln(os.Stderr, "unknown subcommand:", os.Args[1])
		os.Exit(2)
	}
}
