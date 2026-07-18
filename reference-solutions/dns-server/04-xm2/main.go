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

func encodeDomainName(name string) []byte {
	var buf []byte
	for _, label := range splitLabels(name) {
		buf = append(buf, byte(len(label)))
		buf = append(buf, label...)
	}
	buf = append(buf, 0)
	return buf
}

func splitLabels(name string) []string {
	var labels []string
	start := 0
	for i := 0; i <= len(name); i++ {
		if i == len(name) || name[i] == '.' {
			labels = append(labels, name[start:i])
			start = i + 1
		}
	}
	return labels
}

type dnsQuestion struct {
	name  string
	qtype uint16
	class uint16
}

func (q *dnsQuestion) encode() []byte {
	buf := encodeDomainName(q.name)
	tail := make([]byte, 4)
	binary.BigEndian.PutUint16(tail[0:2], q.qtype)
	binary.BigEndian.PutUint16(tail[2:4], q.class)
	return append(buf, tail...)
}

type dnsAnswer struct {
	name  string
	rtype uint16
	class uint16
	ttl   uint32
	rdata []byte
}

func (a *dnsAnswer) encode() []byte {
	buf := encodeDomainName(a.name)
	tail := make([]byte, 8)
	binary.BigEndian.PutUint16(tail[0:2], a.rtype)
	binary.BigEndian.PutUint16(tail[2:4], a.class)
	binary.BigEndian.PutUint32(tail[4:8], a.ttl)
	buf = append(buf, tail...)
	rdlength := make([]byte, 2)
	binary.BigEndian.PutUint16(rdlength, uint16(len(a.rdata)))
	buf = append(buf, rdlength...)
	buf = append(buf, a.rdata...)
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
			id:      1234,
			qr:      1,
			qdcount: 1,
			ancount: 1,
		}
		question := dnsQuestion{
			name:  "codecrafters.io",
			qtype: 1,
			class: 1,
		}
		answer := dnsAnswer{
			name:  "codecrafters.io",
			rtype: 1,
			class: 1,
			ttl:   60,
			rdata: []byte{8, 8, 8, 8},
		}
		response := header.encode()
		response = append(response, question.encode()...)
		response = append(response, answer.encode()...)

		_, err = udpConn.WriteToUDP(response, source)
		if err != nil {
			fmt.Println("Failed to send response:", err)
		}
	}
}
