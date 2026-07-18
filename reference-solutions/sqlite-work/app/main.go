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

func decodeInt(data []byte, serialType int64) int64 {
	switch serialType {
	case 0:
		return 0
	case 1:
		return int64(int8(data[0]))
	case 2:
		return int64(int16(binary.BigEndian.Uint16(data)))
	case 3:
		x := int32(data[0])<<16 | int32(data[1])<<8 | int32(data[2])
		if data[0]&0x80 != 0 {
			x -= 1 << 24
		}
		return int64(x)
	case 4:
		return int64(int32(binary.BigEndian.Uint32(data)))
	case 5:
		var x int64
		for _, b := range data {
			x = x<<8 | int64(b)
		}
		if data[0]&0x80 != 0 {
			x -= 1 << 48
		}
		return x
	case 6:
		return int64(binary.BigEndian.Uint64(data))
	case 8:
		return 0
	case 9:
		return 1
	}
	return 0
}

func pageHeaderOffset(pageNum int) int {
	if pageNum == 1 {
		return 100
	}
	return 0
}

func readTableLeafRecords(file *os.File, pageNum int, pageSize uint16) []record {
	page := readPage(file, pageNum, int(pageSize))
	hdrOff := pageHeaderOffset(pageNum)
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

func readSchemaRecords(file *os.File, pageSize uint16) []record {
	return readTableLeafRecords(file, 1, pageSize)
}

func findTableSchema(schemaRows []record, tableName string) (createSQL string, rootPage int) {
	for _, r := range schemaRows {
		if string(r.values[2]) == tableName {
			rootPage = int(decodeInt(r.values[3], r.serialTypes[3]))
			createSQL = string(r.values[4])
			return
		}
	}
	log.Fatalf("table not found: %s", tableName)
	return
}

func parseColumns(createSQL string) []string {
	open := strings.Index(createSQL, "(")
	close := strings.LastIndex(createSQL, ")")
	inner := createSQL[open+1 : close]
	parts := strings.Split(inner, ",")
	columns := make([]string, len(parts))
	for i, p := range parts {
		fields := strings.Fields(strings.TrimSpace(p))
		columns[i] = fields[0]
	}
	return columns
}

func columnIndex(columns []string, name string) int {
	for i, c := range columns {
		if strings.EqualFold(c, name) {
			return i
		}
	}
	return -1
}

func valueToString(v []byte, serialType int64) string {
	if serialType >= 12 {
		return string(v)
	}
	return fmt.Sprintf("%d", decodeInt(v, serialType))
}

func splitOnKeyword(s, keyword string) (before string, after string) {
	idx := strings.Index(strings.ToUpper(s), keyword)
	if idx == -1 {
		return strings.TrimSpace(s), ""
	}
	return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+len(keyword):])
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

	switch {
	case command == ".dbinfo":
		page := readPage(databaseFile, 1, int(pageSize))
		numTables := binary.BigEndian.Uint16(page[103:105])
		fmt.Printf("database page size: %v\n", pageSize)
		fmt.Printf("number of tables: %v\n", numTables)
	case command == ".tables":
		schemaRows := readSchemaRecords(databaseFile, pageSize)
		var names []string
		for _, r := range schemaRows {
			names = append(names, string(r.values[2]))
		}
		fmt.Println(strings.Join(names, " "))
	case strings.HasPrefix(strings.ToUpper(command), "SELECT COUNT(*)"):
		fields := strings.Fields(command)
		tableName := fields[len(fields)-1]
		schemaRows := readSchemaRecords(databaseFile, pageSize)
		_, rootPage := findTableSchema(schemaRows, tableName)
		rows := readTableLeafRecords(databaseFile, rootPage, pageSize)
		fmt.Println(len(rows))
	case strings.HasPrefix(strings.ToUpper(command), "SELECT "):
		selectPart, fromPart := splitOnKeyword(command[len("SELECT "):], " FROM ")
		columnName := selectPart
		tableName := fromPart

		schemaRows := readSchemaRecords(databaseFile, pageSize)
		createSQL, rootPage := findTableSchema(schemaRows, tableName)
		columns := parseColumns(createSQL)
		colIdx := columnIndex(columns, columnName)

		rows := readTableLeafRecords(databaseFile, rootPage, pageSize)
		for _, r := range rows {
			fmt.Println(valueToString(r.values[colIdx], r.serialTypes[colIdx]))
		}
	default:
		fmt.Println("Unknown command", command)
		os.Exit(1)
	}
}
