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
	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Println("Error accepting connection: ", err.Error())
			os.Exit(1)
		}
		go handleConnection(conn)
	}
}

const (
	apiKeyAPIVersions             = 18
	apiKeyDescribeTopicPartitions = 75
	unsupportedVersion            = 35
)

// supportedAPIs lists the api_keys entries advertised in every successful
// ApiVersions response.
var supportedAPIs = []struct {
	key, minVersion, maxVersion uint16
}{
	{apiKeyAPIVersions, 0, 4},
	{apiKeyDescribeTopicPartitions, 0, 0},
}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	for {
		if err := handleRequest(conn); err != nil {
			return
		}
	}
}

func handleRequest(conn net.Conn) error {
	apiVersion, correlationID, err := readRequestHeader(conn)
	if err != nil {
		return err
	}

	var body []byte
	if apiVersion < 0 || apiVersion > 4 {
		body = binary.BigEndian.AppendUint16(nil, unsupportedVersion)
	} else {
		body = binary.BigEndian.AppendUint16(nil, 0) // error_code: no error

		body = appendUvarint(body, uint64(len(supportedAPIs)+1)) // api_keys compact array length
		for _, api := range supportedAPIs {
			body = binary.BigEndian.AppendUint16(body, api.key)
			body = binary.BigEndian.AppendUint16(body, api.minVersion)
			body = binary.BigEndian.AppendUint16(body, api.maxVersion)
			body = append(body, 0) // TAG_BUFFER (empty)
		}

		body = binary.BigEndian.AppendUint32(body, 0) // throttle_time_ms
		body = append(body, 0)                        // TAG_BUFFER (empty)
	}

	response := make([]byte, 0, 8+len(body))
	response = binary.BigEndian.AppendUint32(response, 0) // message_size (placeholder for now)
	response = binary.BigEndian.AppendUint32(response, correlationID)
	response = append(response, body...)
	binary.BigEndian.PutUint32(response[0:4], uint32(len(response)-4))
	_, err = conn.Write(response)
	return err
}

// appendUvarint appends x encoded as an unsigned LEB128 varint, the
// encoding Kafka's COMPACT_ARRAY/COMPACT_STRING length prefixes use.
func appendUvarint(buf []byte, x uint64) []byte {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], x)
	return append(buf, tmp[:n]...)
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
