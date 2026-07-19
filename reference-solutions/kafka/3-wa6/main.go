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

func handleConnection(conn net.Conn) {
	defer conn.Close()

	correlationID, err := readCorrelationID(conn)
	if err != nil {
		return
	}

	response := make([]byte, 8)
	binary.BigEndian.PutUint32(response[0:4], 0) // message_size (placeholder for this stage)
	binary.BigEndian.PutUint32(response[4:8], correlationID)
	conn.Write(response)
}

// readCorrelationID reads one request off conn and returns the
// correlation_id from its request header v2 (the third field, after the
// 2-byte request_api_key and 2-byte request_api_version).
func readCorrelationID(conn net.Conn) (uint32, error) {
	var sizeBuf [4]byte
	if _, err := io.ReadFull(conn, sizeBuf[:]); err != nil {
		return 0, err
	}
	size := binary.BigEndian.Uint32(sizeBuf[:])

	body := make([]byte, size)
	if _, err := io.ReadFull(conn, body); err != nil {
		return 0, err
	}
	// body: request_api_key(2) request_api_version(2) correlation_id(4) ...
	return binary.BigEndian.Uint32(body[4:8]), nil
}
