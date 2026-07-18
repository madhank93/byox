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
	rowid       int64
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
	pageType := page[hdrOff]
	numCells := binary.BigEndian.Uint16(page[hdrOff+3 : hdrOff+5])

	if pageType == 5 { // interior table b-tree page
		cellPtrStart := hdrOff + 12
		var records []record
		for i := 0; i < int(numCells); i++ {
			ptrOff := cellPtrStart + i*2
			cellOffset := int(binary.BigEndian.Uint16(page[ptrOff : ptrOff+2]))
			childPage := int(binary.BigEndian.Uint32(page[cellOffset : cellOffset+4]))
			records = append(records, readTableLeafRecords(file, childPage, pageSize)...)
		}
		rightMostPage := int(binary.BigEndian.Uint32(page[hdrOff+8 : hdrOff+12]))
		records = append(records, readTableLeafRecords(file, rightMostPage, pageSize)...)
		return records
	}

	cellPtrStart := hdrOff + 8
	var records []record
	for i := 0; i < int(numCells); i++ {
		ptrOff := cellPtrStart + i*2
		cellOffset := int(binary.BigEndian.Uint16(page[ptrOff : ptrOff+2]))
		records = append(records, readTableLeafCell(page, cellOffset))
	}
	return records
}

func readTableLeafCell(page []byte, cellOffset int) record {
	_, n1 := readVarint(page[cellOffset:])
	rowid, n2 := readVarint(page[cellOffset+n1:])
	payloadStart := cellOffset + n1 + n2
	rec := parseRecord(page[payloadStart:])
	rec.rowid = rowid
	return rec
}

// findRowByRowid does a point lookup by rowid, descending only into the
// child subtree that can contain it instead of scanning the whole table.
func findRowByRowid(file *os.File, pageNum int, pageSize uint16, targetRowid int64) (record, bool) {
	page := readPage(file, pageNum, int(pageSize))
	hdrOff := pageHeaderOffset(pageNum)
	pageType := page[hdrOff]
	numCells := binary.BigEndian.Uint16(page[hdrOff+3 : hdrOff+5])

	if pageType == 5 { // interior table b-tree page
		cellPtrStart := hdrOff + 12
		for i := 0; i < int(numCells); i++ {
			ptrOff := cellPtrStart + i*2
			cellOffset := int(binary.BigEndian.Uint16(page[ptrOff : ptrOff+2]))
			childPage := int(binary.BigEndian.Uint32(page[cellOffset : cellOffset+4]))
			key, _ := readVarint(page[cellOffset+4:])
			if targetRowid <= key {
				return findRowByRowid(file, childPage, pageSize, targetRowid)
			}
		}
		rightMostPage := int(binary.BigEndian.Uint32(page[hdrOff+8 : hdrOff+12]))
		return findRowByRowid(file, rightMostPage, pageSize, targetRowid)
	}

	cellPtrStart := hdrOff + 8
	for i := 0; i < int(numCells); i++ {
		ptrOff := cellPtrStart + i*2
		cellOffset := int(binary.BigEndian.Uint16(page[ptrOff : ptrOff+2]))
		rec := readTableLeafCell(page, cellOffset)
		if rec.rowid == targetRowid {
			return rec, true
		}
	}
	return record{}, false
}

// indexScan walks an index b-tree looking for an exact match on target,
// pruning subtrees that cannot contain it, and returns the matching rowids.
func indexScan(file *os.File, pageNum int, pageSize uint16, target string) []int64 {
	page := readPage(file, pageNum, int(pageSize))
	hdrOff := pageHeaderOffset(pageNum)
	pageType := page[hdrOff]
	numCells := binary.BigEndian.Uint16(page[hdrOff+3 : hdrOff+5])
	isInterior := pageType == 2

	cellPtrStart := hdrOff + 8
	if isInterior {
		cellPtrStart = hdrOff + 12
	}

	var rowids []int64
	for i := 0; i < int(numCells); i++ {
		ptrOff := cellPtrStart + i*2
		cellOffset := int(binary.BigEndian.Uint16(page[ptrOff : ptrOff+2]))

		pos := cellOffset
		var childPage int
		if isInterior {
			childPage = int(binary.BigEndian.Uint32(page[pos : pos+4]))
			pos += 4
		}
		payloadSize, n := readVarint(page[pos:])
		pos += n
		rec := parseRecord(page[pos : pos+int(payloadSize)])
		key := string(rec.values[0])

		if isInterior && key >= target {
			rowids = append(rowids, indexScan(file, childPage, pageSize, target)...)
		}
		if key == target {
			last := len(rec.values) - 1
			rowids = append(rowids, decodeInt(rec.values[last], rec.serialTypes[last]))
		}
		if key > target {
			return rowids
		}
	}
	if isInterior {
		rightMostPage := int(binary.BigEndian.Uint32(page[hdrOff+8 : hdrOff+12]))
		rowids = append(rowids, indexScan(file, rightMostPage, pageSize, target)...)
	}
	return rowids
}

type schemaEntry struct {
	typ      string
	tblName  string
	rootPage int
	sql      string
}

func readSchemaEntries(file *os.File, pageSize uint16) []schemaEntry {
	rows := readTableLeafRecords(file, 1, pageSize)
	entries := make([]schemaEntry, len(rows))
	for i, r := range rows {
		entries[i] = schemaEntry{
			typ:      string(r.values[0]),
			tblName:  string(r.values[2]),
			rootPage: int(decodeInt(r.values[3], r.serialTypes[3])),
			sql:      string(r.values[4]),
		}
	}
	return entries
}

func findTableSchema(entries []schemaEntry, tableName string) (createSQL string, rootPage int) {
	for _, e := range entries {
		if e.typ == "table" && e.tblName == tableName {
			return e.sql, e.rootPage
		}
	}
	log.Fatalf("table not found: %s", tableName)
	return
}

// findIndex returns the root page of an index on (tableName, columnName), if one exists.
func findIndex(entries []schemaEntry, tableName, columnName string) (rootPage int, found bool) {
	for _, e := range entries {
		if e.typ != "index" || e.tblName != tableName {
			continue
		}
		idxColumns := parseColumns(e.sql)
		if len(idxColumns) > 0 && strings.EqualFold(idxColumns[0], columnName) {
			return e.rootPage, true
		}
	}
	return 0, false
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

func valueToString(v []byte, serialType int64, rowid int64) string {
	if serialType == 0 {
		// INTEGER PRIMARY KEY columns are stored as NULL in the record;
		// their value is the row's rowid.
		return fmt.Sprintf("%d", rowid)
	}
	if serialType >= 12 {
		return string(v)
	}
	return fmt.Sprintf("%d", decodeInt(v, serialType))
}

func parseWhereClause(s string) (column string, value string) {
	parts := strings.SplitN(s, "=", 2)
	column = strings.TrimSpace(parts[0])
	value = strings.Trim(strings.TrimSpace(parts[1]), "'")
	return
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
		schemaEntries := readSchemaEntries(databaseFile, pageSize)
		var names []string
		for _, e := range schemaEntries {
			if e.typ == "table" {
				names = append(names, e.tblName)
			}
		}
		fmt.Println(strings.Join(names, " "))
	case strings.HasPrefix(strings.ToUpper(command), "SELECT COUNT(*)"):
		fields := strings.Fields(command)
		tableName := fields[len(fields)-1]
		schemaEntries := readSchemaEntries(databaseFile, pageSize)
		_, rootPage := findTableSchema(schemaEntries, tableName)
		rows := readTableLeafRecords(databaseFile, rootPage, pageSize)
		fmt.Println(len(rows))
	case strings.HasPrefix(strings.ToUpper(command), "SELECT "):
		selectPart, fromPart := splitOnKeyword(command[len("SELECT "):], " FROM ")
		columnNames := strings.Split(selectPart, ",")
		for i := range columnNames {
			columnNames[i] = strings.TrimSpace(columnNames[i])
		}
		tableName, wherePart := splitOnKeyword(fromPart, " WHERE ")

		var whereCol, whereVal string
		hasWhere := wherePart != ""
		if hasWhere {
			whereCol, whereVal = parseWhereClause(wherePart)
		}

		schemaEntries := readSchemaEntries(databaseFile, pageSize)
		createSQL, rootPage := findTableSchema(schemaEntries, tableName)
		columns := parseColumns(createSQL)
		colIdxs := make([]int, len(columnNames))
		for i, name := range columnNames {
			colIdxs[i] = columnIndex(columns, name)
		}

		var rows []record
		if indexRootPage, ok := findIndex(schemaEntries, tableName, whereCol); hasWhere && ok {
			for _, rowid := range indexScan(databaseFile, indexRootPage, pageSize, whereVal) {
				if rec, found := findRowByRowid(databaseFile, rootPage, pageSize, rowid); found {
					rows = append(rows, rec)
				}
			}
		} else {
			whereIdx := -1
			if hasWhere {
				whereIdx = columnIndex(columns, whereCol)
			}
			for _, r := range readTableLeafRecords(databaseFile, rootPage, pageSize) {
				if hasWhere && valueToString(r.values[whereIdx], r.serialTypes[whereIdx], r.rowid) != whereVal {
					continue
				}
				rows = append(rows, r)
			}
		}

		for _, r := range rows {
			values := make([]string, len(colIdxs))
			for i, idx := range colIdxs {
				values[i] = valueToString(r.values[idx], r.serialTypes[idx], r.rowid)
			}
			fmt.Println(strings.Join(values, "|"))
		}
	default:
		fmt.Println("Unknown command", command)
		os.Exit(1)
	}
}
