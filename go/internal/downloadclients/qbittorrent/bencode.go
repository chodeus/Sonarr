package qbittorrent

import (
	"crypto/sha1" //nolint:gosec // BitTorrent info-hash is SHA-1 by spec (BEP-3) — not a security boundary.
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
)

// extractInfoHash returns the lowercase-hex SHA-1 of the "info" dict
// from a bencoded .torrent body. BitTorrent BEP-3 defines the
// info-hash as the SHA-1 of the bencoded info dictionary; ALL clients
// (qBittorrent, Transmission, Deluge, etc.) key torrents by this
// value. Mirrors the C# `TorrentFileInfoReader.GetHashFromTorrentFile`
// at `src/NzbDrone.Common/Disk/TorrentFileInfoReader.cs` (and the
// BencodeNET library it delegates to).
//
// The hash is computed against the RAW bencoded bytes of the info
// dict — re-bencoding would break the hash because bencode dict
// keys must be sorted lexicographically and any deviation
// (whitespace, key reordering) changes the digest. So this scanner
// records the byte-range of the info value and hashes that slice
// directly.
//
// Returns ErrBencodeNoInfoDict when the body parses cleanly but
// contains no "info" key; ErrBencodeInvalid for malformed bencode.
//
// Slice 6.H (qBittorrent hygiene long-tail — closes the bencode-hash
// deferral noted as a follow-up in 5.G1e). The scanner is intentionally
// minimal — only the structural skipping that locating the info dict
// requires; no value decoding for non-info keys.
func extractInfoHash(body []byte) (string, error) {
	if len(body) == 0 {
		return "", ErrBencodeInvalid
	}
	if body[0] != 'd' {
		return "", fmt.Errorf("%w: top-level value must be a dict, got %q", ErrBencodeInvalid, body[0])
	}
	// Walk the top-level dict's key/value pairs, looking for "info".
	pos := 1
	for pos < len(body) && body[pos] != 'e' {
		// Read a key (always a byte string in bencode dicts).
		key, next, err := readBencodeString(body, pos)
		if err != nil {
			return "", err
		}
		pos = next
		// Record the start position of the value, scan past it, and
		// hash the slice if the key was "info".
		valueStart := pos
		valueEnd, err := skipBencodeValue(body, pos)
		if err != nil {
			return "", err
		}
		if string(key) == "info" {
			sum := sha1.Sum(body[valueStart:valueEnd]) //nolint:gosec // BEP-3.
			return hex.EncodeToString(sum[:]), nil
		}
		pos = valueEnd
	}
	return "", ErrBencodeNoInfoDict
}

// ErrBencodeInvalid signals malformed bencode. Distinguished from
// [ErrBencodeNoInfoDict] (well-formed but lacks the expected key).
var ErrBencodeInvalid = errors.New("qbittorrent: malformed bencode")

// ErrBencodeNoInfoDict signals well-formed bencode that doesn't
// contain a top-level "info" key. Almost never seen in the wild —
// real .torrent files always carry an info dict — but the
// distinction lets callers branch on "broken torrent file" vs
// "broken bencode parser input".
var ErrBencodeNoInfoDict = errors.New("qbittorrent: bencode has no info dict")

// readBencodeString parses a length-prefixed byte string starting at
// pos. Returns the string bytes, the position immediately after the
// string, and any parse error.
func readBencodeString(body []byte, pos int) ([]byte, int, error) {
	colon := pos
	for colon < len(body) && body[colon] != ':' {
		if body[colon] < '0' || body[colon] > '9' {
			return nil, 0, fmt.Errorf("%w: expected digit before string-length colon at pos %d", ErrBencodeInvalid, colon)
		}
		colon++
	}
	if colon >= len(body) {
		return nil, 0, fmt.Errorf("%w: unterminated string-length prefix", ErrBencodeInvalid)
	}
	length, err := strconv.Atoi(string(body[pos:colon]))
	if err != nil || length < 0 {
		return nil, 0, fmt.Errorf("%w: invalid string length %q", ErrBencodeInvalid, body[pos:colon])
	}
	start := colon + 1
	end := start + length
	if end > len(body) {
		return nil, 0, fmt.Errorf("%w: string runs past end of input", ErrBencodeInvalid)
	}
	return body[start:end], end, nil
}

// skipBencodeValue advances past one bencode value starting at pos.
// Returns the position immediately after the value. Handles all four
// bencode kinds (integer / string / list / dict) recursively for the
// container kinds.
func skipBencodeValue(body []byte, pos int) (int, error) {
	if pos >= len(body) {
		return 0, fmt.Errorf("%w: ran out of input mid-value", ErrBencodeInvalid)
	}
	switch c := body[pos]; {
	case c == 'i':
		// i<digits>e
		end := pos + 1
		for end < len(body) && body[end] != 'e' {
			end++
		}
		if end >= len(body) {
			return 0, fmt.Errorf("%w: unterminated integer", ErrBencodeInvalid)
		}
		return end + 1, nil
	case c == 'l':
		// l<values>e
		pos++
		for pos < len(body) && body[pos] != 'e' {
			next, err := skipBencodeValue(body, pos)
			if err != nil {
				return 0, err
			}
			pos = next
		}
		if pos >= len(body) {
			return 0, fmt.Errorf("%w: unterminated list", ErrBencodeInvalid)
		}
		return pos + 1, nil
	case c == 'd':
		// d<key,value>...e
		pos++
		for pos < len(body) && body[pos] != 'e' {
			// Skip key (always a string).
			_, next, err := readBencodeString(body, pos)
			if err != nil {
				return 0, err
			}
			pos = next
			// Skip value.
			next, err = skipBencodeValue(body, pos)
			if err != nil {
				return 0, err
			}
			pos = next
		}
		if pos >= len(body) {
			return 0, fmt.Errorf("%w: unterminated dict", ErrBencodeInvalid)
		}
		return pos + 1, nil
	case c >= '0' && c <= '9':
		// String — length-prefixed.
		_, next, err := readBencodeString(body, pos)
		return next, err
	default:
		return 0, fmt.Errorf("%w: unexpected token %q at pos %d", ErrBencodeInvalid, c, pos)
	}
}
