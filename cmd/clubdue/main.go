package main

import (
	"log"
	"os"

	"github.com/wmichelin/clubdue/internal/clubdue"
)

func main() {
	if err := clubdue.Run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
