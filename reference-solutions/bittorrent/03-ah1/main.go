package main

import (
	"encoding/json"
	"fmt"
	"os"
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
	default:
		return nil, "", fmt.Errorf("only strings, integers and lists are supported at the moment")
	}
}

func main() {
	// You can use print statements as follows for debugging, they'll be visible when running tests.
	fmt.Fprintln(os.Stderr, "Logs from your program will appear here!")

	command := os.Args[1]

	if command == "decode" {
		bencodedValue := os.Args[2]

		decoded, err := decodeBencode(bencodedValue)
		if err != nil {
			fmt.Println(err)
			return
		}

		jsonOutput, _ := json.Marshal(decoded)
		fmt.Println(string(jsonOutput))
	} else {
		fmt.Println("Unknown command: " + command)
		os.Exit(1)
	}
}
