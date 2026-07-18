package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"strings"
)

func readVarint(data []byte) (int64, int) {
	var result int64
	for i := 0; i < 9; i++ {
		b := data[i]
		if i == 8 {
			result = (result << 8) | int64(b)
			return result, i + 1
		}
		result = (result << 7) | int64(b&0x7f)
		if b&0x80 == 0 {
			return result, i + 1
		}
	}
	return result, 9
}

func serialTypeSize(serialType int64) int {
	switch {
	case serialType == 0, serialType == 8, serialType == 9:
		return 0
	case serialType >= 1 && serialType <= 4:
		return int(serialType)
	case serialType == 5:
		return 6
	case serialType == 6 || serialType == 7:
		return 8
	case serialType >= 12 && serialType%2 == 0:
		return int((serialType - 12) / 2)
	default: // odd >= 13: text
		return int((serialType - 13) / 2)
	}
}

type record struct {
	serialTypes []int64
	values      [][]byte
}

func parseRecord(payload []byte) record {
	headerLen, n := readVarint(payload)
	pos := n
	var serialTypes []int64
	for pos < int(headerLen) {
		st, m := readVarint(payload[pos:])
		serialTypes = append(serialTypes, st)
		pos += m
	}
	values := make([][]byte, len(serialTypes))
	bodyPos := int(headerLen)
	for i, st := range serialTypes {
		size := serialTypeSize(st)
		values[i] = payload[bodyPos : bodyPos+size]
		bodyPos += size
	}
	return record{serialTypes: serialTypes, values: values}
}

func readPage(file *os.File, pageNum int, pageSize int) []byte {
	buf := make([]byte, pageSize)
	offset := int64(pageNum-1) * int64(pageSize)
	if _, err := file.ReadAt(buf, offset); err != nil {
		log.Fatal(err)
	}
	return buf
}

func readDatabaseHeader(file *os.File) uint16 {
	header := make([]byte, 100)
	if _, err := file.ReadAt(header, 0); err != nil {
		log.Fatal(err)
	}
	return binary.BigEndian.Uint16(header[16:18])
}

func readSchemaRecords(file *os.File, pageSize uint16) []record {
	page := readPage(file, 1, int(pageSize))
	hdrOff := 100
	numCells := binary.BigEndian.Uint16(page[hdrOff+3 : hdrOff+5])
	cellPtrStart := hdrOff + 8

	var records []record
	for i := 0; i < int(numCells); i++ {
		ptrOff := cellPtrStart + i*2
		cellOffset := int(binary.BigEndian.Uint16(page[ptrOff : ptrOff+2]))
		_, n1 := readVarint(page[cellOffset:])
		_, n2 := readVarint(page[cellOffset+n1:])
		payloadStart := cellOffset + n1 + n2
		records = append(records, parseRecord(page[payloadStart:]))
	}
	return records
}

func main() {
	databaseFilePath := os.Args[1]
	command := os.Args[2]

	databaseFile, err := os.Open(databaseFilePath)
	if err != nil {
		log.Fatal(err)
	}
	defer databaseFile.Close()

	pageSize := readDatabaseHeader(databaseFile)

	switch command {
	case ".dbinfo":
		page := readPage(databaseFile, 1, int(pageSize))
		numTables := binary.BigEndian.Uint16(page[103:105])
		fmt.Printf("database page size: %v\n", pageSize)
		fmt.Printf("number of tables: %v\n", numTables)
	case ".tables":
		schemaRows := readSchemaRecords(databaseFile, pageSize)
		var names []string
		for _, r := range schemaRows {
			names = append(names, string(r.values[2]))
		}
		fmt.Println(strings.Join(names, " "))
	default:
		fmt.Println("Unknown command", command)
		os.Exit(1)
	}
}
