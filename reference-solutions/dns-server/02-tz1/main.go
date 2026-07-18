package main

import (
	"encoding/binary"
	"fmt"
	"net"
)

type dnsHeader struct {
	id      uint16
	qr      uint8
	opcode  uint8
	aa      uint8
	tc      uint8
	rd      uint8
	ra      uint8
	z       uint8
	rcode   uint8
	qdcount uint16
	ancount uint16
	nscount uint16
	arcount uint16
}

func (h *dnsHeader) encode() []byte {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint16(buf[0:2], h.id)

	var flags uint16
	flags |= uint16(h.qr) << 15
	flags |= uint16(h.opcode) << 11
	flags |= uint16(h.aa) << 10
	flags |= uint16(h.tc) << 9
	flags |= uint16(h.rd) << 8
	flags |= uint16(h.ra) << 7
	flags |= uint16(h.z) << 4
	flags |= uint16(h.rcode)
	binary.BigEndian.PutUint16(buf[2:4], flags)

	binary.BigEndian.PutUint16(buf[4:6], h.qdcount)
	binary.BigEndian.PutUint16(buf[6:8], h.ancount)
	binary.BigEndian.PutUint16(buf[8:10], h.nscount)
	binary.BigEndian.PutUint16(buf[10:12], h.arcount)

	return buf
}

func main() {
	fmt.Println("Logs from your program will appear here!")

	udpAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:2053")
	if err != nil {
		fmt.Println("Failed to resolve UDP address:", err)
		return
	}

	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		fmt.Println("Failed to bind to address:", err)
		return
	}
	defer udpConn.Close()

	buf := make([]byte, 512)

	for {
		size, source, err := udpConn.ReadFromUDP(buf)
		if err != nil {
			fmt.Println("Error receiving data:", err)
			break
		}

		receivedData := string(buf[:size])
		fmt.Printf("Received %d bytes from %s: %s\n", size, source, receivedData)

		header := dnsHeader{
			id: 1234,
			qr: 1,
		}
		response := header.encode()

		_, err = udpConn.WriteToUDP(response, source)
		if err != nil {
			fmt.Println("Failed to send response:", err)
		}
	}
}
