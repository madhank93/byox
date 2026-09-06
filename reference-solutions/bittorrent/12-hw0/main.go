package main

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	// bencode "github.com/jackpal/bencode-go" // Available if you need it!
)

// Ensures gofmt doesn't remove the "os" encoding/json import (feel free to remove this!)
var _ = json.Marshal

// myPeerID identifies this client in tracker requests and peer handshakes,
// generated once per run (a fixed, hardcoded ID is asking for
// "Connection reset by peer" collisions with other clients using the same
// hardcoded value). The "-CC0001-" prefix follows the common convention of
// tagging the client/version, filled out to the required 20 bytes with
// random suffix bytes.
var myPeerID = generateMyPeerID()

func generateMyPeerID() [20]byte {
	var id [20]byte
	copy(id[:], "-CC0001-")
	rand.Read(id[8:])
	return id
}

// Example:
// - 5:hello -> hello
// - 10:hello12345 -> hello12345
// - i52e -> 52
// - i-52e -> -52
// - l5:helloi52ee -> ["hello", 52]
func decodeBencode(bencodedString string) (interface{}, error) {
	value, _, err := decodeValue(bencodedString)
	return value, err
}

// decodeValue decodes the single bencoded value at the start of s and
// returns it along with whatever's left unconsumed, so a caller decoding a
// sequence of back-to-back values (a list's elements) can keep decoding
// from where the previous element ended.
func decodeValue(s string) (interface{}, string, error) {
	switch {
	case len(s) > 0 && unicode.IsDigit(rune(s[0])):
		colonIndex := strings.IndexByte(s, ':')
		if colonIndex == -1 {
			return nil, "", fmt.Errorf("invalid bencoded string: missing ':'")
		}
		length, err := strconv.Atoi(s[:colonIndex])
		if err != nil {
			return nil, "", err
		}
		start := colonIndex + 1
		return s[start : start+length], s[start+length:], nil
	case len(s) > 0 && s[0] == 'i':
		endIndex := strings.IndexByte(s, 'e')
		if endIndex == -1 {
			return nil, "", fmt.Errorf("invalid bencoded integer: missing terminating 'e'")
		}
		n, err := strconv.Atoi(s[1:endIndex])
		return n, s[endIndex+1:], err
	case len(s) > 0 && s[0] == 'l':
		list := []interface{}{}
		rest := s[1:]
		for len(rest) > 0 && rest[0] != 'e' {
			var v interface{}
			var err error
			v, rest, err = decodeValue(rest)
			if err != nil {
				return nil, "", err
			}
			list = append(list, v)
		}
		if len(rest) == 0 {
			return nil, "", fmt.Errorf("invalid bencoded list: missing terminating 'e'")
		}
		return list, rest[1:], nil
	case len(s) > 0 && s[0] == 'd':
		dict := map[string]interface{}{}
		rest := s[1:]
		for len(rest) > 0 && rest[0] != 'e' {
			var key, value interface{}
			var err error
			key, rest, err = decodeValue(rest)
			if err != nil {
				return nil, "", err
			}
			keyStr, ok := key.(string)
			if !ok {
				return nil, "", fmt.Errorf("invalid bencoded dictionary: key is not a string")
			}
			value, rest, err = decodeValue(rest)
			if err != nil {
				return nil, "", err
			}
			dict[keyStr] = value
		}
		if len(rest) == 0 {
			return nil, "", fmt.Errorf("invalid bencoded dictionary: missing terminating 'e'")
		}
		// json.Marshal sorts map[string]interface{} keys alphabetically, which
		// matches bencode's required lexicographic key order — no extra work.
		return dict, rest[1:], nil
	default:
		return nil, "", fmt.Errorf("only strings, integers, lists and dictionaries are supported at the moment")
	}
}

// --- Magnet links: BEP 9 metadata exchange over BEP 10 extensions --------

const (
	// extensionSupportByte/Bit is the 20th bit from the right of the eight
	// reserved handshake bytes — the flag that says "I speak BEP 10".
	extensionSupportByte = 5
	extensionSupportBit  = 0x10

	// extHandshakeID is the extension message id reserved for the extension
	// handshake itself; every other id is assigned by the receiving peer.
	extHandshakeID = 0

	// myMetadataExtensionID is the id this client asks peers to use when they
	// send it ut_metadata messages. Any value from 1 to 255 works — the two
	// sides pick independently, and each uses the number the *other* one
	// advertised.
	myMetadataExtensionID = 1

	extMetadataRequest = 0
	extMetadataData    = 1
	extMetadataReject  = 2

	// metadataPieceSize is the 16 KiB chunk the metadata is split into. Every
	// torrent in this challenge fits in one chunk.
	metadataPieceSize = 16 * 1024
)

// magnetLink is the little a magnet URI carries: enough to find peers and to
// check that what they send back is the right torrent, and nothing else.
type magnetLink struct {
	Tracker  string
	InfoHash [20]byte
	Name     string
}

// parseMagnetLink parses a v1 magnet URI. Only xt is required; a link with no
// tracker cannot be used to find peers here, so it is rejected rather than
// failing later with an empty URL.
func parseMagnetLink(link string) (magnetLink, error) {
	var m magnetLink

	const prefix = "magnet:?"
	if !strings.HasPrefix(link, prefix) {
		return m, fmt.Errorf("not a magnet link: %q", link)
	}
	query, err := url.ParseQuery(strings.TrimPrefix(link, prefix))
	if err != nil {
		return m, err
	}

	const btih = "urn:btih:"
	xt := query.Get("xt")
	if !strings.HasPrefix(xt, btih) {
		return m, fmt.Errorf("magnet link has no %s info hash", btih)
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(xt, btih))
	if err != nil {
		return m, fmt.Errorf("info hash: %w", err)
	}
	if len(raw) != 20 {
		return m, fmt.Errorf("info hash is %d bytes, want 20", len(raw))
	}
	copy(m.InfoHash[:], raw)

	m.Tracker = query.Get("tr")
	if m.Tracker == "" {
		return m, fmt.Errorf("magnet link has no tracker")
	}
	m.Name = query.Get("dn")
	return m, nil
}

// writeExtendedMessage sends one BEP 10 message: the ordinary peer message id
// 20, then the extension's own id, then its bencoded payload.
func writeExtendedMessage(conn net.Conn, extensionID byte, payload string) error {
	body := make([]byte, 0, 1+len(payload))
	body = append(body, extensionID)
	body = append(body, payload...)
	return writePeerMessage(conn, msgExtended, body)
}

// awaitBitfield reads until the peer's bitfield arrives. A peer may interleave
// other traffic before it, and a peer that already knows we support extensions
// may even send its extension handshake first, so anything unexpected is
// collected and handed back rather than treated as an error.
func awaitBitfield(conn net.Conn) (early []*peerMessage, err error) {
	for {
		msg, err := readPeerMessage(conn)
		if err != nil {
			return nil, err
		}
		if msg == nil {
			continue // keep-alive
		}
		if msg.ID == msgBitfield {
			return early, nil
		}
		early = append(early, msg)
	}
}

// magnetSession is a peer connection taken through both handshakes, holding
// the one thing the extension handshake was for: the id this peer wants
// ut_metadata messages addressed to.
type magnetSession struct {
	conn                net.Conn
	peerID              [20]byte
	metadataExtensionID byte
	pending             []*peerMessage
}

func (s *magnetSession) Close() { s.conn.Close() }

// openMagnetSession dials a peer and performs the base handshake with the
// extension bit set, then — only if the peer set that bit too — the extension
// handshake, in the order BEP 10 lays out: bitfield first, then extensions.
func openMagnetSession(addr string, infoHash [20]byte) (*magnetSession, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}

	var reserved [8]byte
	reserved[extensionSupportByte] = extensionSupportBit

	peerID, peerReserved, err := performHandshakeWithReserved(conn, infoHash, reserved)
	if err != nil {
		conn.Close()
		return nil, err
	}
	session := &magnetSession{conn: conn, peerID: peerID}

	early, err := awaitBitfield(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	session.pending = early

	// A peer that never claimed extension support must not be sent extension
	// messages — that is what keeps older clients working.
	if peerReserved[extensionSupportByte]&extensionSupportBit == 0 {
		return session, nil
	}

	handshake, err := encodeBencode(map[string]interface{}{
		"m": map[string]interface{}{"ut_metadata": myMetadataExtensionID},
	})
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := writeExtendedMessage(conn, extHandshakeID, handshake); err != nil {
		conn.Close()
		return nil, err
	}

	payload, err := session.awaitExtended(extHandshakeID)
	if err != nil {
		conn.Close()
		return nil, err
	}
	id, err := metadataExtensionID(payload)
	if err != nil {
		conn.Close()
		return nil, err
	}
	session.metadataExtensionID = id
	return session, nil
}

// awaitExtended reads until an extension message addressed to wantID arrives,
// returning the payload that follows the extension id. Messages banked while
// waiting for the bitfield are consulted first.
func (s *magnetSession) awaitExtended(wantID byte) ([]byte, error) {
	for {
		var msg *peerMessage
		if len(s.pending) > 0 {
			msg, s.pending = s.pending[0], s.pending[1:]
		} else {
			var err error
			if msg, err = readPeerMessage(s.conn); err != nil {
				return nil, err
			}
			if msg == nil {
				continue // keep-alive
			}
		}
		if msg.ID != msgExtended || len(msg.Payload) == 0 || msg.Payload[0] != wantID {
			continue // ordinary peer traffic, or another extension's message
		}
		return msg.Payload[1:], nil
	}
}

// metadataExtensionID pulls m.ut_metadata out of an extension handshake
// payload. A peer that omits it does not speak the metadata extension.
func metadataExtensionID(payload []byte) (byte, error) {
	decoded, err := decodeBencode(string(payload))
	if err != nil {
		return 0, err
	}
	dict, ok := decoded.(map[string]interface{})
	if !ok {
		return 0, fmt.Errorf("extension handshake is not a dictionary")
	}
	m, ok := dict["m"].(map[string]interface{})
	if !ok {
		return 0, fmt.Errorf("extension handshake has no \"m\" dictionary")
	}
	id, ok := m["ut_metadata"].(int)
	if !ok {
		return 0, fmt.Errorf("peer does not support ut_metadata")
	}
	if id < 1 || id > 255 {
		return 0, fmt.Errorf("ut_metadata id %d is out of range", id)
	}
	return byte(id), nil
}

// fetchMetadata asks the peer for the torrent's info dict and checks what came
// back: the info hash from the magnet link is the only thing that makes an
// untrusted peer's metadata safe to act on.
func (s *magnetSession) fetchMetadata(infoHash [20]byte) (map[string]interface{}, error) {
	if s.metadataExtensionID == 0 {
		return nil, fmt.Errorf("peer does not support the metadata extension")
	}

	request, err := encodeBencode(map[string]interface{}{
		"msg_type": extMetadataRequest,
		"piece":    0,
	})
	if err != nil {
		return nil, err
	}
	if err := writeExtendedMessage(s.conn, s.metadataExtensionID, request); err != nil {
		return nil, err
	}

	// The peer addresses its reply with the id we advertised, not with its own.
	payload, err := s.awaitExtended(myMetadataExtensionID)
	if err != nil {
		return nil, err
	}

	// The metadata follows the bencoded header with nothing marking the join,
	// so the decoder's leftover input is what separates them.
	header, rest, err := decodeValue(string(payload))
	if err != nil {
		return nil, err
	}
	dict, ok := header.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("metadata message header is not a dictionary")
	}
	switch dict["msg_type"] {
	case extMetadataData:
	case extMetadataReject:
		return nil, fmt.Errorf("peer rejected the metadata request")
	default:
		return nil, fmt.Errorf("unexpected metadata msg_type %v", dict["msg_type"])
	}
	if size, ok := dict["total_size"].(int); ok {
		if size > len(rest) {
			return nil, fmt.Errorf("metadata truncated: want %d bytes, got %d", size, len(rest))
		}
		rest = rest[:size]
	}

	if sha1.Sum([]byte(rest)) != infoHash {
		return nil, fmt.Errorf("metadata does not match the magnet link's info hash")
	}
	decoded, err := decodeBencode(rest)
	if err != nil {
		return nil, err
	}
	info, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("metadata is not a dictionary")
	}
	return info, nil
}

// magnetMetadata walks a magnet link all the way to a usable info dict: parse
// the link, ask the tracker for peers, then take the first peer that will
// complete both handshakes and serve the metadata.
func magnetMetadata(link string) (magnetLink, map[string]interface{}, []string, error) {
	magnet, err := parseMagnetLink(link)
	if err != nil {
		return magnet, nil, nil, err
	}
	// The tracker wants a "left" byte count and the whole point of the request
	// is to learn it, so announce one nominal piece's worth.
	peers, err := discoverPeersAt(magnet.Tracker, magnet.InfoHash, metadataPieceSize)
	if err != nil {
		return magnet, nil, nil, err
	}

	var lastErr error
	for _, addr := range peers {
		session, err := openMagnetSession(addr, magnet.InfoHash)
		if err != nil {
			lastErr = err
			continue
		}
		info, err := session.fetchMetadata(magnet.InfoHash)
		session.Close()
		if err != nil {
			lastErr = err
			continue
		}
		return magnet, info, peers, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no peers available")
	}
	return magnet, nil, nil, lastErr
}

func main() {
	// You can use print statements as follows for debugging, they'll be visible when running tests.
	fmt.Fprintln(os.Stderr, "Logs from your program will appear here!")

	command := os.Args[1]

	switch command {
	case "decode":
		bencodedValue := os.Args[2]

		decoded, err := decodeBencode(bencodedValue)
		if err != nil {
			fmt.Println(err)
			return
		}

		jsonOutput, _ := json.Marshal(decoded)
		fmt.Println(string(jsonOutput))
	case "info":
		torrent, info, infoHash, err := parseTorrentFile(os.Args[2])
		if err != nil {
			fmt.Println(err)
			return
		}
		printTorrentInfo(torrent, info, infoHash)
	case "peers":
		torrent, info, infoHash, err := parseTorrentFile(os.Args[2])
		if err != nil {
			fmt.Println(err)
			return
		}
		peers, err := discoverPeers(torrent, info, infoHash)
		if err != nil {
			fmt.Println(err)
			return
		}
		for _, p := range peers {
			fmt.Println(p)
		}
	case "handshake":
		_, _, infoHash, err := parseTorrentFile(os.Args[2])
		if err != nil {
			fmt.Println(err)
			return
		}
		conn, err := net.Dial("tcp", os.Args[3])
		if err != nil {
			fmt.Println(err)
			return
		}
		defer conn.Close()

		peerID, err := performHandshake(conn, infoHash)
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Printf("Peer ID: %x\n", peerID)
	case "download_piece":
		outputPath := os.Args[3]
		torrentPath := os.Args[4]
		pieceIndex, err := strconv.Atoi(os.Args[5])
		if err != nil {
			fmt.Println(err)
			return
		}

		piece, err := downloadPieceFromTorrent(torrentPath, pieceIndex)
		if err != nil {
			fmt.Println(err)
			return
		}
		if err := os.WriteFile(outputPath, piece, 0644); err != nil {
			fmt.Println(err)
			return
		}
		fmt.Printf("Piece %d downloaded to %s.\n", pieceIndex, outputPath)
	case "download":
		outputPath := os.Args[3]
		torrentPath := os.Args[4]

		file, err := downloadFileFromTorrent(torrentPath)
		if err != nil {
			fmt.Println(err)
			return
		}
		if err := os.WriteFile(outputPath, file, 0644); err != nil {
			fmt.Println(err)
			return
		}
		fmt.Printf("Downloaded %s to %s.\n", torrentPath, outputPath)
	case "magnet_parse":
		magnet, err := parseMagnetLink(os.Args[2])
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Printf("Tracker URL: %s\n", magnet.Tracker)
		fmt.Printf("Info Hash: %x\n", magnet.InfoHash)
	case "magnet_handshake":
		magnet, err := parseMagnetLink(os.Args[2])
		if err != nil {
			fmt.Println(err)
			return
		}
		peers, err := discoverPeersAt(magnet.Tracker, magnet.InfoHash, metadataPieceSize)
		if err != nil {
			fmt.Println(err)
			return
		}
		if len(peers) == 0 {
			fmt.Println("no peers available")
			return
		}
		session, err := openMagnetSession(peers[0], magnet.InfoHash)
		if err != nil {
			fmt.Println(err)
			return
		}
		defer session.Close()

		fmt.Printf("Peer ID: %x\n", session.peerID)
		if session.metadataExtensionID != 0 {
			fmt.Printf("Peer Metadata Extension ID: %d\n", session.metadataExtensionID)
		}
	case "magnet_info":
		magnet, info, _, err := magnetMetadata(os.Args[2])
		if err != nil {
			fmt.Println(err)
			return
		}
		printTorrentInfo(map[string]interface{}{"announce": magnet.Tracker}, info, magnet.InfoHash)
	case "magnet_download_piece":
		outputPath := os.Args[3]
		pieceIndex, err := strconv.Atoi(os.Args[5])
		if err != nil {
			fmt.Println(err)
			return
		}
		magnet, info, peers, err := magnetMetadata(os.Args[4])
		if err != nil {
			fmt.Println(err)
			return
		}
		piece, err := downloadOnePiece(info, magnet.InfoHash, peers, pieceIndex)
		if err != nil {
			fmt.Println(err)
			return
		}
		if err := os.WriteFile(outputPath, piece, 0644); err != nil {
			fmt.Println(err)
			return
		}
		fmt.Printf("Piece %d downloaded to %s.\n", pieceIndex, outputPath)
	case "magnet_download":
		outputPath := os.Args[3]
		magnetURI := os.Args[4]

		magnet, info, peers, err := magnetMetadata(magnetURI)
		if err != nil {
			fmt.Println(err)
			return
		}
		file, err := downloadAllPieces(info, magnet.InfoHash, peers)
		if err != nil {
			fmt.Println(err)
			return
		}
		if err := os.WriteFile(outputPath, file, 0644); err != nil {
			fmt.Println(err)
			return
		}
		fmt.Printf("Downloaded %s to %s.\n", magnetURI, outputPath)
	default:
		fmt.Println("Unknown command: " + command)
		os.Exit(1)
	}
}

// parseTorrentFile reads and decodes the .torrent file at path, returning
// the top-level torrent dict, its nested info dict, and the info dict's
// SHA-1 hash (raw 20 bytes, not hex) computed over the info dict's
// re-bencoded form.
func parseTorrentFile(path string) (torrent, info map[string]interface{}, infoHash [20]byte, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, infoHash, err
	}

	decoded, err := decodeBencode(string(data))
	if err != nil {
		return nil, nil, infoHash, err
	}

	torrent = decoded.(map[string]interface{})
	info = torrent["info"].(map[string]interface{})

	infoEncoded, err := encodeBencode(info)
	if err != nil {
		return nil, nil, infoHash, err
	}
	infoHash = sha1.Sum([]byte(infoEncoded))
	return torrent, info, infoHash, nil
}

// printTorrentInfo prints an already-parsed torrent's tracker URL, length,
// info hash, and piece hashes.
func printTorrentInfo(torrent, info map[string]interface{}, infoHash [20]byte) {
	fmt.Printf("Tracker URL: %s\n", torrent["announce"])
	fmt.Printf("Length: %d\n", info["length"])
	fmt.Printf("Info Hash: %x\n", infoHash)

	fmt.Printf("Piece Length: %d\n", info["piece length"])
	fmt.Println("Piece Hashes:")
	pieces := info["pieces"].(string)
	for i := 0; i < len(pieces); i += 20 {
		fmt.Printf("%x\n", pieces[i:i+20])
	}
}

// discoverPeers asks the torrent's tracker for a list of peers, returning
// each as "ip:port".
func discoverPeers(torrent, info map[string]interface{}, infoHash [20]byte) ([]string, error) {
	return discoverPeersAt(torrent["announce"].(string), infoHash, info["length"].(int))
}

// discoverPeersAt is discoverPeers for a caller that has a tracker URL and an
// info hash but no info dict yet — the position a magnet link starts from,
// where the file length that "left" wants is exactly what the tracker is
// being asked to help find out. Announcing a nominal "left" is enough for the
// tracker to answer with peers.
func discoverPeersAt(announce string, infoHash [20]byte, left int) ([]string, error) {
	trackerURL, err := url.Parse(announce)
	if err != nil {
		return nil, err
	}

	query := url.Values{}
	query.Set("info_hash", string(infoHash[:]))
	query.Set("peer_id", string(myPeerID[:]))
	query.Set("port", "6881")
	query.Set("uploaded", "0")
	query.Set("downloaded", "0")
	query.Set("left", strconv.Itoa(left))
	query.Set("compact", "1")
	trackerURL.RawQuery = query.Encode()

	resp, err := http.Get(trackerURL.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	decoded, err := decodeBencode(string(body))
	if err != nil {
		return nil, err
	}
	peersField := decoded.(map[string]interface{})["peers"].(string)

	var peers []string
	for i := 0; i+6 <= len(peersField); i += 6 {
		ip := net.IP([]byte(peersField[i : i+4]))
		port := binary.BigEndian.Uint16([]byte(peersField[i+4 : i+6]))
		peers = append(peers, fmt.Sprintf("%s:%d", ip.String(), port))
	}
	return peers, nil
}

// encodeBencode is the inverse of decodeValue: it bencodes a value decoded
// by this program back into its wire form. Needed because the info hash is
// computed over the info dict's bencoded bytes, not its decoded Go form —
// map[string]interface{} keys are sorted lexicographically to reproduce
// bencode's required (and, for the hash, load-bearing) key ordering.
func encodeBencode(value interface{}) (string, error) {
	switch v := value.(type) {
	case string:
		return fmt.Sprintf("%d:%s", len(v), v), nil
	case int:
		return fmt.Sprintf("i%de", v), nil
	case []interface{}:
		var sb strings.Builder
		sb.WriteByte('l')
		for _, item := range v {
			encoded, err := encodeBencode(item)
			if err != nil {
				return "", err
			}
			sb.WriteString(encoded)
		}
		sb.WriteByte('e')
		return sb.String(), nil
	case map[string]interface{}:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var sb strings.Builder
		sb.WriteByte('d')
		for _, k := range keys {
			keyEncoded, _ := encodeBencode(k)
			sb.WriteString(keyEncoded)
			valEncoded, err := encodeBencode(v[k])
			if err != nil {
				return "", err
			}
			sb.WriteString(valEncoded)
		}
		sb.WriteByte('e')
		return sb.String(), nil
	default:
		return "", fmt.Errorf("cannot bencode value of type %T", value)
	}
}

// performHandshake sends the BitTorrent peer handshake over conn and
// returns the remote peer's ID from its handshake response.
func performHandshake(conn net.Conn, infoHash [20]byte) ([20]byte, error) {
	peerID, _, err := performHandshakeWithReserved(conn, infoHash, [8]byte{})
	return peerID, err
}

// performHandshakeWithReserved is performHandshake with the eight reserved
// bytes under the caller's control, and the peer's own reserved bytes handed
// back. Those bytes are how the two sides advertise optional protocol
// features to each other before any message is exchanged.
func performHandshakeWithReserved(conn net.Conn, infoHash [20]byte, reserved [8]byte) ([20]byte, [8]byte, error) {
	var peerID [20]byte
	var peerReserved [8]byte

	handshake := make([]byte, 0, 68)
	handshake = append(handshake, 19)
	handshake = append(handshake, "BitTorrent protocol"...)
	handshake = append(handshake, reserved[:]...)
	handshake = append(handshake, infoHash[:]...)
	handshake = append(handshake, myPeerID[:]...)

	if _, err := conn.Write(handshake); err != nil {
		return peerID, peerReserved, err
	}

	response := make([]byte, 68)
	if _, err := io.ReadFull(conn, response); err != nil {
		return peerID, peerReserved, err
	}
	copy(peerReserved[:], response[20:28])
	copy(peerID[:], response[48:68])
	return peerID, peerReserved, nil
}

// Peer wire protocol message IDs (see BEP 0003's "peer messages" section).
const (
	msgChoke      = 0
	msgUnchoke    = 1
	msgInterested = 2
	msgBitfield   = 5
	msgRequest    = 6
	msgPiece      = 7
	blockSize     = 16 * 1024

	// pipelineDepth is how many block requests stay in flight per peer. Five
	// is the figure the BitTorrent spec's own notes suggest for saturating a
	// link without flooding a peer's request queue.
	pipelineDepth = 5

	// msgExtended carries every message defined by an extension (BEP 10);
	// the first payload byte then says which extension it belongs to.
	msgExtended = 20
)

type peerMessage struct {
	ID      byte
	Payload []byte
}

// readPeerMessage reads one length-prefixed peer message off conn. A nil
// message with a nil error means a keep-alive (zero-length message, no id)
// was read — distinct from any real message, including one with id 0
// (choke), so callers must check for nil rather than a zero-valued struct.
func readPeerMessage(conn net.Conn) (*peerMessage, error) {
	lengthBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lengthBuf); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(lengthBuf)
	if length == 0 {
		return nil, nil // keep-alive
	}
	// io.ReadFull loops internally until it has read exactly len(body)
	// bytes (or hits an error/EOF first), which is what handles a message
	// arriving over several partial TCP reads instead of one.
	body := make([]byte, length)
	if _, err := io.ReadFull(conn, body); err != nil {
		return nil, err
	}
	return &peerMessage{ID: body[0], Payload: body[1:]}, nil
}

// writePeerMessage sends one length-prefixed peer message on conn.
func writePeerMessage(conn net.Conn, id byte, payload []byte) error {
	buf := make([]byte, 4+1+len(payload))
	binary.BigEndian.PutUint32(buf[0:4], uint32(1+len(payload)))
	buf[4] = id
	copy(buf[5:], payload)
	_, err := conn.Write(buf)
	return err
}

// waitForMessage reads messages off conn until one with the given id
// arrives (skipping keep-alives), or returns an error if a message with a
// different, unexpected id shows up first.
func waitForMessage(conn net.Conn, wantID byte) (*peerMessage, error) {
	for {
		msg, err := readPeerMessage(conn)
		if err != nil {
			return nil, err
		}
		if msg == nil {
			continue // keep-alive
		}
		if msg.ID != wantID {
			return nil, fmt.Errorf("expected message id %d, got %d", wantID, msg.ID)
		}
		return msg, nil
	}
}

// pieceLength returns the actual length of the piece at index, which is
// shorter than info's "piece length" for the last piece whenever the total
// file length isn't an exact multiple of it.
func pieceLength(info map[string]interface{}, index int) int {
	totalLength := info["length"].(int)
	normalLength := info["piece length"].(int)
	numPieces := (totalLength + normalLength - 1) / normalLength
	if index == numPieces-1 {
		if remainder := totalLength % normalLength; remainder != 0 {
			return remainder
		}
	}
	return normalLength
}

// awaitReadyToDownload does the one-time-per-connection handshake
// follow-up (bitfield/interested/unchoke) required before any piece can be
// requested. The peer sends its bitfield once, right after the handshake -
// not before every piece - so callers downloading multiple pieces over the
// same connection must call this exactly once, not per piece.
func awaitReadyToDownload(conn net.Conn) error {
	if _, err := waitForMessage(conn, msgBitfield); err != nil {
		return err
	}
	if err := writePeerMessage(conn, msgInterested, nil); err != nil {
		return err
	}
	if _, err := waitForMessage(conn, msgUnchoke); err != nil {
		return err
	}
	return nil
}

// downloadPiece runs the request/piece exchange over an already-handshaken
// (and already awaitReadyToDownload'd) conn to fetch one piece, split into
// blockSize-byte block requests.
//
// Requests are pipelined: up to pipelineDepth of them are in flight at once,
// so the next block is already travelling while the current one is read. One
// request per round trip is correct but latency-bound, and a whole file that
// way overruns the tester's per-stage timeout.
func downloadPiece(conn net.Conn, pieceIndex, length int) ([]byte, error) {
	piece := make([]byte, length)
	numBlocks := (length + blockSize - 1) / blockSize

	sent, received := 0, 0
	for received < numBlocks {
		for sent < numBlocks && sent-received < pipelineDepth {
			offset := sent * blockSize
			blockLen := blockSize
			if offset+blockLen > length {
				blockLen = length - offset
			}

			payload := make([]byte, 12)
			binary.BigEndian.PutUint32(payload[0:4], uint32(pieceIndex))
			binary.BigEndian.PutUint32(payload[4:8], uint32(offset))
			binary.BigEndian.PutUint32(payload[8:12], uint32(blockLen))
			if err := writePeerMessage(conn, msgRequest, payload); err != nil {
				return nil, err
			}
			sent++
		}

		// Blocks can come back in any order, so the payload's own offset
		// decides where they land, not the order they were asked for.
		msg, err := waitForMessage(conn, msgPiece)
		if err != nil {
			return nil, err
		}
		blockOffset := binary.BigEndian.Uint32(msg.Payload[4:8])
		if int(blockOffset) > length {
			return nil, fmt.Errorf("piece %d: block offset %d past piece end", pieceIndex, blockOffset)
		}
		copy(piece[blockOffset:], msg.Payload[8:])
		received++
	}
	return piece, nil
}

// downloadPieceFromTorrent does the full flow for one piece: parse the
// torrent, ask the tracker for peers, then fetch and verify the piece.
func downloadPieceFromTorrent(torrentPath string, pieceIndex int) ([]byte, error) {
	torrent, info, infoHash, err := parseTorrentFile(torrentPath)
	if err != nil {
		return nil, err
	}
	peers, err := discoverPeers(torrent, info, infoHash)
	if err != nil {
		return nil, err
	}
	return downloadOnePiece(info, infoHash, peers, pieceIndex)
}

// downloadOnePiece fetches one piece from the first peer willing to serve it,
// verifying it against its hash from the info dict. Peers come and go, so a
// failure on one is a reason to try the next rather than to give up.
func downloadOnePiece(info map[string]interface{}, infoHash [20]byte, peers []string, pieceIndex int) ([]byte, error) {
	if len(peers) == 0 {
		return nil, fmt.Errorf("no peers available")
	}
	pieceHashes := info["pieces"].(string)

	var lastErr error
	for _, addr := range peers {
		conn, err := connectToPeer(addr, infoHash)
		if err != nil {
			lastErr = err
			continue
		}
		piece, err := downloadPiece(conn, pieceIndex, pieceLength(info, pieceIndex))
		conn.Close()
		if err != nil {
			lastErr = err
			continue
		}
		actual := sha1.Sum(piece)
		if string(actual[:]) != pieceHashes[pieceIndex*20:pieceIndex*20+20] {
			lastErr = fmt.Errorf("piece %d hash mismatch", pieceIndex)
			continue
		}
		return piece, nil
	}
	return nil, lastErr
}

// connectToPeer dials addr and takes it all the way to ready-to-request:
// handshake, bitfield, interested, unchoke.
func connectToPeer(addr string, infoHash [20]byte) (net.Conn, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	if _, err := performHandshake(conn, infoHash); err != nil {
		conn.Close()
		return nil, err
	}
	if err := awaitReadyToDownload(conn); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// downloadFileFromTorrent parses the torrent, asks the tracker for peers and
// downloads the whole file.
func downloadFileFromTorrent(torrentPath string) ([]byte, error) {
	torrent, info, infoHash, err := parseTorrentFile(torrentPath)
	if err != nil {
		return nil, err
	}
	peers, err := discoverPeers(torrent, info, infoHash)
	if err != nil {
		return nil, err
	}
	return downloadAllPieces(info, infoHash, peers)
}

// downloadAllPieces downloads every piece of the torrent described by info,
// verifying each against its hash before it counts, and returns them
// concatenated in order.
//
// Pieces are spread over all the peers the tracker returned, one worker per
// peer pulling indexes off a shared queue. A single peer is fast enough for
// one piece but not for a whole file inside the tester's timeout, and peers
// vary in speed, so a queue rather than a fixed split keeps the slow ones from
// holding the download up.
func downloadAllPieces(info map[string]interface{}, infoHash [20]byte, peers []string) ([]byte, error) {
	if len(peers) == 0 {
		return nil, fmt.Errorf("no peers available")
	}

	pieceHashes := info["pieces"].(string)
	numPieces := len(pieceHashes) / 20

	// Buffered to numPieces so a worker handing a piece back after a failure
	// can never block, which is what would otherwise deadlock the pool.
	jobs := make(chan int, numPieces)
	for i := 0; i < numPieces; i++ {
		jobs <- i
	}

	// Each index is claimed by one worker at a time, so writes never overlap.
	pieces := make([][]byte, numPieces)
	remaining := int64(numPieces)

	var wg sync.WaitGroup
	for _, addr := range peers {
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()

			conn, err := connectToPeer(addr, infoHash)
			if err != nil {
				return // this peer is unusable; the others carry the load
			}
			defer conn.Close()

			for index := range jobs {
				piece, err := downloadPiece(conn, index, pieceLength(info, index))
				if err == nil {
					actual := sha1.Sum(piece)
					if string(actual[:]) != pieceHashes[index*20:index*20+20] {
						err = fmt.Errorf("piece %d hash mismatch", index)
					}
				}
				if err != nil {
					// The connection is out of step with what was requested on
					// it, so drop it and let another peer retry the piece.
					jobs <- index
					return
				}

				pieces[index] = piece
				if atomic.AddInt64(&remaining, -1) == 0 {
					close(jobs)
				}
			}
		}(addr)
	}
	wg.Wait()

	file := make([]byte, 0, info["length"].(int))
	for i, piece := range pieces {
		if piece == nil {
			return nil, fmt.Errorf("piece %d: no peer could supply it", i)
		}
		file = append(file, piece...)
	}
	return file, nil
}
