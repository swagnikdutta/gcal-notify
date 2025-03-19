package main

import (
	"log"
	"time"
)

func parseTime(t string) (time.Time, error) {
	parsedTime, err := time.Parse(time.RFC3339, t)
	if err != nil {
		log.Printf("Error parsing time: %s\n", err)
	}
	return parsedTime, err
}
