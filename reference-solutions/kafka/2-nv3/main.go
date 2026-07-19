package main

import (
	"encoding/binary"
	"fmt"
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

	response := make([]byte, 8)
	binary.BigEndian.PutUint32(response[0:4], 0) // message_size (placeholder for this stage)
	binary.BigEndian.PutUint32(response[4:8], 7) // correlation_id (hardcoded for this stage)
	conn.Write(response)
}
