package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
)

func main() {
	l, err := net.Listen("tcp", "0.0.0.0:6379")
	if err != nil {
		fmt.Println("Failed to bind to port 6379")
		os.Exit(1)
	}
	defer l.Close()

	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Println("Error accepting connection:", err)
			continue
		}
		go handleConn(conn)
	}
}

// handleConn answers every command on one connection until the client
// disconnects. One goroutine per connection is Go's answer to Redis's event
// loop; from here on, anything shared between connections needs guarding.
func handleConn(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				fmt.Println("read error:", err)
			}
			return
		}
		if strings.HasPrefix(strings.ToUpper(line), "PING") {
			conn.Write([]byte("+PONG\r\n"))
		}
	}
}
