package main

import (
	"fmt"
	"net"
	"os"
)

func main() {
	l, err := net.Listen("tcp", "0.0.0.0:6379")
	if err != nil {
		fmt.Println("Failed to bind to port 6379")
		os.Exit(1)
	}
	defer l.Close()

	conn, err := l.Accept()
	if err != nil {
		fmt.Println("Error accepting connection:", err)
		os.Exit(1)
	}
	defer conn.Close()

	buf := make([]byte, 512)
	if _, err := conn.Read(buf); err != nil {
		return
	}
	conn.Write([]byte("+PONG\r\n"))
}
