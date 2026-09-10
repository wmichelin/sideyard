package main

import (
	"log"
	"os"

	"github.com/wmichelin/clubdue/internal/app"
)

func main() {
	if err := app.Run(os.Args[1:]); err != nil {
		log.Print(err)
		os.Exit(app.ExitCode(err))
	}
}
