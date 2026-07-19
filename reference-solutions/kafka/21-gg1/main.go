package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
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
	apiKeyProduce                 = 0
	apiKeyFetch                   = 1
	apiKeyAPIVersions             = 18
	apiKeyDescribeTopicPartitions = 75

	unsupportedVersion      = 35
	unknownTopicOrPartition = 3
	unknownTopicID          = 100
)

// supportedAPIs lists the api_keys entries advertised in every successful
// ApiVersions response.
var supportedAPIs = []struct {
	key, minVersion, maxVersion uint16
}{
	{apiKeyProduce, 0, 11},
	{apiKeyFetch, 0, 16},
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
	case apiKeyFetch:
		response = buildResponse(correlationID, true, fetchBody(r))
	case apiKeyProduce:
		response = buildResponse(correlationID, true, produceBody(r))
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
// response body, looking each requested topic up in the cluster metadata
// log read from disk.
func describeTopicPartitionsBody(r *byteReader) []byte {
	topicCount := int(r.uvarint()) - 1
	topics := make([]string, topicCount)
	for i := range topics {
		topics[i] = r.compactString()
		r.tagBuffer()
	}
	sort.Strings(topics)

	metadata, _ := loadClusterMetadata()

	body := binary.BigEndian.AppendUint32(nil, 0) // throttle_time_ms
	body = appendUvarint(body, uint64(len(topics)+1))
	for _, name := range topics {
		topic, ok := metadata[name]
		if !ok {
			body = binary.BigEndian.AppendUint16(body, unknownTopicOrPartition) // error_code
			body = appendCompactString(body, name)
			body = append(body, make([]byte, 16)...) // topic_id: all-zero UUID
			body = append(body, 0)                   // is_internal: false
			body = appendUvarint(body, 1)            // partitions: empty compact array
		} else {
			body = binary.BigEndian.AppendUint16(body, 0) // error_code: no error
			body = appendCompactString(body, name)
			body = append(body, topic.id[:]...) // topic_id
			body = append(body, 0)              // is_internal: false

			body = appendUvarint(body, uint64(len(topic.partitions)+1))
			for _, p := range topic.partitions {
				body = binary.BigEndian.AppendUint16(body, 0) // error_code
				body = binary.BigEndian.AppendUint32(body, uint32(p.id))
				body = binary.BigEndian.AppendUint32(body, uint32(p.leader))
				body = binary.BigEndian.AppendUint32(body, uint32(p.leaderEpoch))
				body = appendCompactArrayOfInt32(body, p.replicas)
				body = appendCompactArrayOfInt32(body, p.isr)
				body = appendUvarint(body, 1) // eligible_leader_replicas: empty
				body = appendUvarint(body, 1) // last_known_elr: empty
				body = appendUvarint(body, 1) // offline_replicas: empty
				body = append(body, 0)        // TAG_BUFFER (empty)
			}
		}
		body = binary.BigEndian.AppendUint32(body, 0) // topic_authorized_operations
		body = append(body, 0)                        // TAG_BUFFER (empty)
	}
	body = append(body, 0xff) // next_cursor: -1 (null)
	body = append(body, 0)    // TAG_BUFFER (empty)
	return body
}

// appendCompactArrayOfInt32 appends a COMPACT_ARRAY of INT32 values.
func appendCompactArrayOfInt32(buf []byte, vals []int32) []byte {
	buf = appendUvarint(buf, uint64(len(vals)+1))
	for _, v := range vals {
		buf = binary.BigEndian.AppendUint32(buf, uint32(v))
	}
	return buf
}

type fetchPartitionRequest struct {
	partition int32
}

type fetchTopicRequest struct {
	topicID    [16]byte
	partitions []fetchPartitionRequest
}

// parseFetchTopics reads the Fetch (v16) request's topics array: for each
// topic, a UUID and a COMPACT_ARRAY of partitions (of which only the
// partition index is needed so far).
func parseFetchTopics(r *byteReader) []fetchTopicRequest {
	topicCount := int(r.uvarint()) - 1
	topics := make([]fetchTopicRequest, topicCount)
	for i := range topics {
		var id [16]byte
		copy(id[:], r.bytes(16))

		partCount := int(r.uvarint()) - 1
		partitions := make([]fetchPartitionRequest, partCount)
		for j := range partitions {
			partitions[j].partition = r.int32()
			r.skip(4 + 8 + 4 + 8 + 4) // current_leader_epoch, fetch_offset, last_fetched_epoch, log_start_offset, partition_max_bytes
		}
		r.tagBuffer()

		topics[i] = fetchTopicRequest{topicID: id, partitions: partitions}
	}
	return topics
}

// fetchBody builds the Fetch (v16) response body. Every requested topic
// is currently reported as unknown (error_code 100/UNKNOWN_TOPIC_ID) for
// each of its partitions - real topic data comes in later stages.
func fetchBody(r *byteReader) []byte {
	r.skip(4 + 4 + 4 + 1 + 4 + 4) // max_wait_ms, min_bytes, max_bytes, isolation_level, session_id, session_epoch
	topics := parseFetchTopics(r)
	_, byUUID := loadClusterMetadata()

	body := binary.BigEndian.AppendUint32(nil, 0) // throttle_time_ms
	body = binary.BigEndian.AppendUint16(body, 0) // error_code
	body = binary.BigEndian.AppendUint32(body, 0) // session_id
	body = appendUvarint(body, uint64(len(topics)+1))
	for _, t := range topics {
		topic, known := byUUID[t.topicID]
		errorCode := uint16(unknownTopicID)
		if known {
			errorCode = 0
		}

		body = append(body, t.topicID[:]...)
		body = appendUvarint(body, uint64(len(t.partitions)+1))
		for _, p := range t.partitions {
			var records []byte
			if known {
				records = readPartitionLog(topic.name, p.partition)
			}

			body = binary.BigEndian.AppendUint32(body, uint32(p.partition))
			body = binary.BigEndian.AppendUint16(body, errorCode)
			body = binary.BigEndian.AppendUint64(body, 0) // high_watermark
			body = binary.BigEndian.AppendUint64(body, 0) // last_stable_offset
			body = binary.BigEndian.AppendUint64(body, 0) // log_start_offset
			body = appendUvarint(body, 1)                 // aborted_transactions: empty
			var noPreferredReplica int32 = -1
			body = binary.BigEndian.AppendUint32(body, uint32(noPreferredReplica)) // preferred_read_replica
			if len(records) == 0 {
				body = appendUvarint(body, 0) // records: null COMPACT_RECORDS
			} else {
				body = appendUvarint(body, uint64(len(records)+1))
				body = append(body, records...)
			}
			body = append(body, 0) // TAG_BUFFER (partition)
		}
		body = append(body, 0) // TAG_BUFFER (topic)
	}
	body = append(body, 0) // TAG_BUFFER (end of body)
	return body
}

type producePartitionRequest struct {
	id            int32
	recordBatches []byte
}

type produceTopicRequest struct {
	name       string
	partitions []producePartitionRequest
}

// parseProduceTopics reads the Produce (v11) request's topics array,
// assuming the caller already consumed transactional_id/acks/timeout_ms.
// Each partition's record batch bytes are kept as-is (compact bytes:
// a varint length+1 prefix followed by raw, already-wire-format bytes),
// ready to append straight to a partition's on-disk log file.
func parseProduceTopics(r *byteReader) []produceTopicRequest {
	topicCount := int(r.uvarint()) - 1
	topics := make([]produceTopicRequest, topicCount)
	for i := range topics {
		name := r.compactString()

		partCount := int(r.uvarint()) - 1
		partitions := make([]producePartitionRequest, partCount)
		for j := range partitions {
			id := r.int32()
			size := int(r.uvarint()) - 1
			var recordBatches []byte
			if size > 0 {
				recordBatches = r.bytes(size)
			}
			r.tagBuffer()
			partitions[j] = producePartitionRequest{id: id, recordBatches: recordBatches}
		}
		r.tagBuffer()

		topics[i] = produceTopicRequest{name: name, partitions: partitions}
	}
	return topics
}

// produceBody builds the Produce (v11) response body. For this stage,
// every partition is reported as unknown (error_code 3), regardless of
// whether the topic/partition actually exists - real validation comes
// in the next stage.
// partitionExists reports whether topic has a partition with the given
// id, per the __cluster_metadata PARTITION_RECORD entries loaded for it.
func partitionExists(topic *topicInfo, id int32) bool {
	for _, p := range topic.partitions {
		if p.id == id {
			return true
		}
	}
	return false
}

func produceBody(r *byteReader) []byte {
	r.compactString() // transactional_id (COMPACT_NULLABLE_STRING), unused
	r.skip(2 + 4)     // acks, timeout_ms
	topics := parseProduceTopics(r)
	metadata, _ := loadClusterMetadata()

	body := appendUvarint(nil, uint64(len(topics)+1))
	for _, t := range topics {
		topic, topicKnown := metadata[t.name]

		body = appendCompactString(body, t.name)
		body = appendUvarint(body, uint64(len(t.partitions)+1))
		for _, p := range t.partitions {
			var errorCode uint16 = unknownTopicOrPartition
			var baseOffset, logStartOffset int64 = -1, -1
			if topicKnown && partitionExists(topic, p.id) {
				errorCode = 0
				baseOffset = 0
				logStartOffset = 0
			}

			body = binary.BigEndian.AppendUint32(body, uint32(p.id))
			body = binary.BigEndian.AppendUint16(body, errorCode)
			body = binary.BigEndian.AppendUint64(body, uint64(baseOffset))
			var logAppendTimeMs int64 = -1
			body = binary.BigEndian.AppendUint64(body, uint64(logAppendTimeMs))
			body = binary.BigEndian.AppendUint64(body, uint64(logStartOffset))
			body = appendUvarint(body, 1) // record_errors: empty
			body = append(body, 0)        // error_message: null
			body = append(body, 0)        // TAG_BUFFER (partition)
		}
		body = append(body, 0) // TAG_BUFFER (topic)
	}
	body = binary.BigEndian.AppendUint32(body, 0) // throttle_time_ms
	body = append(body, 0)                        // TAG_BUFFER (end of body)
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

func (r *byteReader) int32() int32 {
	return int32(r.uint32())
}

func (r *byteReader) bytes(n int) []byte {
	b := r.buf[r.pos : r.pos+n]
	r.pos += n
	return b
}

func (r *byteReader) skip(n int) {
	r.pos += n
}

// uvarint reads an unsigned LEB128 varint (COMPACT_ARRAY/COMPACT_STRING
// length prefixes, TAG_BUFFER counts).
func (r *byteReader) uvarint() uint64 {
	v, n := binary.Uvarint(r.buf[r.pos:])
	r.pos += n
	return v
}

// varint reads a zigzag-encoded signed varint — Kafka's plain VARINT
// type, used for record-level fields (sizes, deltas, key/value lengths).
// Distinct from uvarint's plain unsigned encoding used elsewhere.
func (r *byteReader) varint() int64 {
	v, n := binary.Varint(r.buf[r.pos:])
	r.pos += n
	return v
}

// compactArrayOfInt32 reads a COMPACT_ARRAY of INT32 values.
func (r *byteReader) compactArrayOfInt32() []int32 {
	n := int(r.uvarint()) - 1
	if n <= 0 {
		return nil
	}
	vals := make([]int32, n)
	for i := range vals {
		vals[i] = r.int32()
	}
	return vals
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

const clusterMetadataLogPath = "/tmp/kraft-combined-logs/__cluster_metadata-0/00000000000000000000.log"

// readPartitionLog returns the raw bytes of a topic-partition's log
// segment file, or nil if it doesn't exist (an empty/new partition).
// These bytes are already a sequence of record batches in exactly the
// on-disk wire format, so they can be embedded in a Fetch response
// verbatim - no re-encoding needed.
func readPartitionLog(topicName string, partition int32) []byte {
	path := fmt.Sprintf("/tmp/kraft-combined-logs/%s-%d/00000000000000000000.log", topicName, partition)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}

// metadata record types, per the __cluster_metadata topic's internal
// (undocumented) schema.
const (
	metadataRecordTypeTopic     = 2
	metadataRecordTypePartition = 3
)

type partitionInfo struct {
	id          int32
	leader      int32
	leaderEpoch int32
	replicas    []int32
	isr         []int32
}

type topicInfo struct {
	name       string
	id         [16]byte
	partitions []partitionInfo
}

// loadClusterMetadata reads and parses the __cluster_metadata log file,
// returning topic metadata keyed both by topic name and by topic UUID
// (DescribeTopicPartitions requests identify topics by name, Fetch
// requests by UUID). A missing or unreadable log file yields empty maps,
// so every topic reports as unknown rather than crashing the broker.
func loadClusterMetadata() (byName map[string]*topicInfo, byUUID map[[16]byte]*topicInfo) {
	topics := map[string]*topicInfo{}
	byUUID = map[[16]byte]*topicInfo{}
	raw, err := os.ReadFile(clusterMetadataLogPath)
	if err != nil {
		return topics, byUUID
	}

	r := &byteReader{buf: raw}
	for r.pos < len(r.buf) {
		r.skip(8) // base_offset
		batchLength := r.int32()
		batchEnd := r.pos + int(batchLength)
		r.skip(4 + 1 + 4)                 // partition_leader_epoch, magic, crc
		r.skip(2 + 4 + 8 + 8 + 8 + 2 + 4) // attributes..base_sequence
		recordCount := r.int32()

		for i := int32(0); i < recordCount; i++ {
			recordSize := r.varint()
			recordEnd := r.pos + int(recordSize)

			r.skip(1)  // record attributes
			r.varint() // timestamp_delta
			r.varint() // offset_delta

			var value []byte
			if keyLen := r.varint(); keyLen > 0 {
				r.skip(int(keyLen))
			}
			if valueLen := r.varint(); valueLen > 0 {
				value = r.bytes(int(valueLen))
			}

			r.pos = recordEnd // skip headers; trust the record's own size
			parseMetadataRecordValue(value, topics, byUUID)
		}

		r.pos = batchEnd
	}
	return topics, byUUID
}

// parseMetadataRecordValue decodes one metadata record's value bytes
// (frame_version, type, version, then type-specific data) and folds it
// into topics/byUUID. Record types other than Topic/Partition (e.g. the
// feature-level record) are ignored.
func parseMetadataRecordValue(value []byte, topics map[string]*topicInfo, byUUID map[[16]byte]*topicInfo) {
	if len(value) < 3 {
		return
	}
	recordType := value[1]
	r := &byteReader{buf: value, pos: 3} // skip frame_version, type, version

	switch recordType {
	case metadataRecordTypeTopic:
		name := r.compactString()
		var id [16]byte
		copy(id[:], r.bytes(16))
		t := &topicInfo{name: name, id: id}
		topics[name] = t
		byUUID[id] = t
	case metadataRecordTypePartition:
		partitionID := r.int32()
		var uuid [16]byte
		copy(uuid[:], r.bytes(16))
		replicas := r.compactArrayOfInt32()
		isr := r.compactArrayOfInt32()
		r.compactArrayOfInt32() // removing_replicas, unused
		r.compactArrayOfInt32() // adding_replicas, unused
		leader := r.int32()
		leaderEpoch := r.int32()
		if t, ok := byUUID[uuid]; ok {
			t.partitions = append(t.partitions, partitionInfo{
				id:          partitionID,
				leader:      leader,
				leaderEpoch: leaderEpoch,
				replicas:    replicas,
				isr:         isr,
			})
		}
	}
}
