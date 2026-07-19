package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
)

func main() {
	fmt.Println("Logs from your program will appear here!")

	l, err := net.Listen("tcp", "0.0.0.0:9092")
	if err != nil {
		fmt.Println("Failed to bind to port 9092")
		os.Exit(1)
	}
	conn, err := l.Accept()
	if err != nil {
		fmt.Println("Error accepting connection: ", err.Error())
		os.Exit(1)
	}
	handleConnection(conn)
}

const unsupportedVersion = 35

func handleConnection(conn net.Conn) {
	defer conn.Close()

	apiVersion, correlationID, err := readRequestHeader(conn)
	if err != nil {
		return
	}

	var errorCode uint16
	if apiVersion < 0 || apiVersion > 4 {
		errorCode = unsupportedVersion
	}

	response := make([]byte, 10)
	binary.BigEndian.PutUint32(response[0:4], 0) // message_size (placeholder for this stage)
	binary.BigEndian.PutUint32(response[4:8], correlationID)
	binary.BigEndian.PutUint16(response[8:10], errorCode)
	conn.Write(response)
}

// readRequestHeader reads one request off conn and returns the
// request_api_version and correlation_id from its request header v2 (the
// first three fields).
func readRequestHeader(conn net.Conn) (apiVersion int16, correlationID uint32, err error) {
	var sizeBuf [4]byte
	if _, err = io.ReadFull(conn, sizeBuf[:]); err != nil {
		return 0, 0, err
	}
	size := binary.BigEndian.Uint32(sizeBuf[:])

	body := make([]byte, size)
	if _, err = io.ReadFull(conn, body); err != nil {
		return 0, 0, err
	}
	// body: request_api_key(2) request_api_version(2) correlation_id(4) ...
	apiVersion = int16(binary.BigEndian.Uint16(body[2:4]))
	correlationID = binary.BigEndian.Uint32(body[4:8])
	return apiVersion, correlationID, nil
}
