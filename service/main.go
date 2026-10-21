// Command ghreport собирает сводку по открытым issue репозиториев GitHub.
package main

import (
	"os"

	"ghreport/internal/app"
)

func main() {
	os.Exit(app.Run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}
