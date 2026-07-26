package main

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"
	// bencode "github.com/jackpal/bencode-go" // Available if you need it!
)

// Ensures gofmt doesn't remove the "os" encoding/json import (feel free to remove this!)
var _ = json.Marshal

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
		printTorrentInfo(os.Args[2])
	default:
		fmt.Println("Unknown command: " + command)
		os.Exit(1)
	}
}

// printTorrentInfo reads and decodes the .torrent file at path and prints
// its tracker URL and file length.
func printTorrentInfo(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Println(err)
		return
	}

	decoded, err := decodeBencode(string(data))
	if err != nil {
		fmt.Println(err)
		return
	}

	torrent := decoded.(map[string]interface{})
	info := torrent["info"].(map[string]interface{})

	fmt.Printf("Tracker URL: %s\n", torrent["announce"])
	fmt.Printf("Length: %d\n", info["length"])

	infoEncoded, err := encodeBencode(info)
	if err != nil {
		fmt.Println(err)
		return
	}
	infoHash := sha1.Sum([]byte(infoEncoded))
	fmt.Printf("Info Hash: %x\n", infoHash)

	fmt.Printf("Piece Length: %d\n", info["piece length"])
	fmt.Println("Piece Hashes:")
	pieces := info["pieces"].(string)
	for i := 0; i < len(pieces); i += 20 {
		fmt.Printf("%x\n", pieces[i:i+20])
	}
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
