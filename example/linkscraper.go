package main

import (
	"encoding/json"
	"github.com/vansante/linkscraper"
	"log"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		panic("Please give an url as argument")
	}
	urlStr := os.Args[1]

	log.Printf("Gathering statistics for link %s", urlStr)

	scraper, err := linkscraper.New(urlStr)
	if err != nil {
		panic(err)
	}
	err = scraper.Start()
	if err != nil {
		panic(err)
	}

	data, err := json.MarshalIndent(scraper.Visited(), "", "  ")
	if err != nil {
		panic(err)
	}

	log.Print(string(data))
}
