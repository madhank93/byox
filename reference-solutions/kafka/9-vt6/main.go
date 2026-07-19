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

	unsupportedVersion      = 35
	unknownTopicOrPartition = 3
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
	apiKey, apiVersion, correlationID, r, err := readRequest(conn)
	if err != nil {
		return err
	}

	var response []byte
	switch apiKey {
	case apiKeyDescribeTopicPartitions:
		response = buildResponse(correlationID, true, describeTopicPartitionsBody(r))
	default: // apiKeyAPIVersions
		response = buildResponse(correlationID, false, apiVersionsBody(apiVersion))
	}

	_, err = conn.Write(response)
	return err
}

// apiVersionsBody builds the ApiVersions response body: an error_code
// (UNSUPPORTED_VERSION unless apiVersion is in 0-4) followed by the
// api_keys/throttle_time_ms fields when the version is supported.
func apiVersionsBody(apiVersion int16) []byte {
	if apiVersion < 0 || apiVersion > 4 {
		return binary.BigEndian.AppendUint16(nil, unsupportedVersion)
	}

	body := binary.BigEndian.AppendUint16(nil, 0) // error_code: no error
	body = appendUvarint(body, uint64(len(supportedAPIs)+1))
	for _, api := range supportedAPIs {
		body = binary.BigEndian.AppendUint16(body, api.key)
		body = binary.BigEndian.AppendUint16(body, api.minVersion)
		body = binary.BigEndian.AppendUint16(body, api.maxVersion)
		body = append(body, 0) // TAG_BUFFER (empty)
	}
	body = binary.BigEndian.AppendUint32(body, 0) // throttle_time_ms
	body = append(body, 0)                        // TAG_BUFFER (empty)
	return body
}

// describeTopicPartitionsBody builds the DescribeTopicPartitions (v0)
// response body. Every topic is currently reported as unknown: for this
// stage the broker doesn't track any real topic metadata yet.
func describeTopicPartitionsBody(r *byteReader) []byte {
	topicCount := int(r.uvarint()) - 1
	topics := make([]string, topicCount)
	for i := range topics {
		topics[i] = r.compactString()
		r.tagBuffer()
	}

	body := binary.BigEndian.AppendUint32(nil, 0) // throttle_time_ms
	body = appendUvarint(body, uint64(len(topics)+1))
	for _, name := range topics {
		body = binary.BigEndian.AppendUint16(body, unknownTopicOrPartition) // error_code
		body = appendCompactString(body, name)
		body = append(body, make([]byte, 16)...)      // topic_id: all-zero UUID
		body = append(body, 0)                        // is_internal: false
		body = appendUvarint(body, 1)                 // partitions: empty compact array
		body = binary.BigEndian.AppendUint32(body, 0) // topic_authorized_operations
		body = append(body, 0)                        // TAG_BUFFER (empty)
	}
	body = append(body, 0xff) // next_cursor: -1 (null)
	body = append(body, 0)    // TAG_BUFFER (empty)
	return body
}

// buildResponse assembles a full response: a 4-byte message_size (filled
// in from the actual length), the correlation_id, an optional response
// header v1 TAG_BUFFER, and the body.
func buildResponse(correlationID uint32, headerTagBuffer bool, body []byte) []byte {
	response := make([]byte, 0, 9+len(body))
	response = binary.BigEndian.AppendUint32(response, 0) // message_size (placeholder)
	response = binary.BigEndian.AppendUint32(response, correlationID)
	if headerTagBuffer {
		response = append(response, 0)
	}
	response = append(response, body...)
	binary.BigEndian.PutUint32(response[0:4], uint32(len(response)-4))
	return response
}

// appendUvarint appends x encoded as an unsigned LEB128 varint, the
// encoding Kafka's COMPACT_ARRAY/COMPACT_STRING length prefixes use.
func appendUvarint(buf []byte, x uint64) []byte {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], x)
	return append(buf, tmp[:n]...)
}

// appendCompactString appends s as a Kafka COMPACT_STRING: an unsigned
// varint of len(s)+1, followed by the raw bytes.
func appendCompactString(buf []byte, s string) []byte {
	buf = appendUvarint(buf, uint64(len(s)+1))
	return append(buf, s...)
}

// byteReader is a cursor over a single request's raw bytes (everything
// after the 4-byte message_size), used to pull out header and body
// fields in order.
type byteReader struct {
	buf []byte
	pos int
}

func (r *byteReader) int16() int16 {
	v := int16(binary.BigEndian.Uint16(r.buf[r.pos : r.pos+2]))
	r.pos += 2
	return v
}

func (r *byteReader) uint32() uint32 {
	v := binary.BigEndian.Uint32(r.buf[r.pos : r.pos+4])
	r.pos += 4
	return v
}

func (r *byteReader) bytes(n int) []byte {
	b := r.buf[r.pos : r.pos+n]
	r.pos += n
	return b
}

// uvarint reads an unsigned LEB128 varint (COMPACT_ARRAY/COMPACT_STRING
// length prefixes, TAG_BUFFER counts).
func (r *byteReader) uvarint() uint64 {
	v, n := binary.Uvarint(r.buf[r.pos:])
	r.pos += n
	return v
}

// nullableString reads a NULLABLE_STRING (INT16 length, or -1 for null,
// followed by that many bytes) — the encoding request header v2's
// client_id uses.
func (r *byteReader) nullableString() string {
	n := r.int16()
	if n < 0 {
		return ""
	}
	return string(r.bytes(int(n)))
}

// compactString reads a COMPACT_STRING: an unsigned varint of length+1
// (0 means null), followed by that many bytes.
func (r *byteReader) compactString() string {
	n := r.uvarint()
	if n == 0 {
		return ""
	}
	return string(r.bytes(int(n - 1)))
}

// tagBuffer reads (and, for now, assumes empty and discards) a
// TAGGED_FIELDS section: an unsigned varint field count, which the
// tester's requests always send as 0 in this course.
func (r *byteReader) tagBuffer() {
	r.uvarint()
}

// readRequest reads one length-prefixed request off conn, parses its
// header v2 (api key, api version, correlation ID, client ID, tag
// buffer), and returns a byteReader positioned at the start of the
// request body for API-specific parsing.
func readRequest(conn net.Conn) (apiKey, apiVersion int16, correlationID uint32, r *byteReader, err error) {
	var sizeBuf [4]byte
	if _, err = io.ReadFull(conn, sizeBuf[:]); err != nil {
		return 0, 0, 0, nil, err
	}
	size := binary.BigEndian.Uint32(sizeBuf[:])

	raw := make([]byte, size)
	if _, err = io.ReadFull(conn, raw); err != nil {
		return 0, 0, 0, nil, err
	}

	r = &byteReader{buf: raw}
	apiKey = r.int16()
	apiVersion = r.int16()
	correlationID = uint32(r.uint32())
	r.nullableString() // client_id, unused
	r.tagBuffer()
	return apiKey, apiVersion, correlationID, r, nil
}
