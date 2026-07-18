package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"
)

func main() {
	databaseFilePath := os.Args[1]
	command := os.Args[2]

	switch command {
	case ".dbinfo":
		databaseFile, err := os.Open(databaseFilePath)
		if err != nil {
			log.Fatal(err)
		}

		header := make([]byte, 100)
		if _, err := databaseFile.Read(header); err != nil {
			log.Fatal(err)
		}

		pageSize := binary.BigEndian.Uint16(header[16:18])

		fmt.Printf("database page size: %v\n", pageSize)
	default:
		fmt.Println("Unknown command", command)
		os.Exit(1)
	}
}
