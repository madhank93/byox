package main

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
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
	trackerURL, err := url.Parse(torrent["announce"].(string))
	if err != nil {
		return nil, err
	}

	query := url.Values{}
	query.Set("info_hash", string(infoHash[:]))
	query.Set("peer_id", string(myPeerID[:]))
	query.Set("port", "6881")
	query.Set("uploaded", "0")
	query.Set("downloaded", "0")
	query.Set("left", strconv.Itoa(info["length"].(int)))
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
	var peerID [20]byte

	handshake := make([]byte, 0, 68)
	handshake = append(handshake, 19)
	handshake = append(handshake, "BitTorrent protocol"...)
	handshake = append(handshake, make([]byte, 8)...) // reserved
	handshake = append(handshake, infoHash[:]...)
	handshake = append(handshake, myPeerID[:]...)

	if _, err := conn.Write(handshake); err != nil {
		return peerID, err
	}

	response := make([]byte, 68)
	if _, err := io.ReadFull(conn, response); err != nil {
		return peerID, err
	}
	copy(peerID[:], response[48:68])
	return peerID, nil
}
