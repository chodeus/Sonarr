package qbittorrent

import (
	"crypto/sha1" //nolint:gosec // BitTorrent info-hash is SHA-1 by spec.
	"encoding/hex"
	"strings"
	"testing"
)

// hashOf returns the lowercase-hex SHA-1 of s — used to build the
// expected info-hash for synthetic bencode fixtures.
func hashOf(infoBytes string) string {
	sum := sha1.Sum([]byte(infoBytes)) //nolint:gosec // BEP-3.
	return hex.EncodeToString(sum[:])
}

func TestExtractInfoHash_SimpleFixture(t *testing.T) {
	t.Parallel()
	// Top-level dict: {"announce": "tracker", "info": {"name": "hello"}}.
	// Bencode dicts are sorted lexicographically; "announce" < "info".
	infoDict := "d4:name5:helloe"
	body := "d8:announce7:tracker4:info" + infoDict + "e"
	got, err := extractInfoHash([]byte(body))
	if err != nil {
		t.Fatalf("extractInfoHash: %v", err)
	}
	if want := hashOf(infoDict); got != want {
		t.Errorf("hash = %q, want %q", got, want)
	}
}

func TestExtractInfoHash_NestedDictsAndListsInInfo(t *testing.T) {
	t.Parallel()
	// Info dict with a nested "files" list of dicts — exercise the
	// recursive skipBencodeValue scan past the right end-of-info.
	// Build by composition so the bencode chars are visibly correct.
	fileA := "d6:lengthi100e4:pathl4:dir15:file1ee"
	fileB := "d6:lengthi200e4:pathl4:dir25:file2ee"
	filesList := "l" + fileA + fileB + "e"
	infoDict := "d5:files" + filesList + "4:name7:foldern" + "e"
	body := "d4:info" + infoDict + "e"
	got, err := extractInfoHash([]byte(body))
	if err != nil {
		t.Fatalf("extractInfoHash: %v", err)
	}
	if want := hashOf(infoDict); got != want {
		t.Errorf("hash = %q, want %q", got, want)
	}
}

func TestExtractInfoHash_InfoNotFirstKey(t *testing.T) {
	t.Parallel()
	// Multiple keys before "info" — verify the scanner advances past
	// each, including a list value and an integer value. Bencode dict
	// keys are sorted lexicographically, so "info" sits between
	// "creation date" and "nodes".
	infoDict := "d4:name1:xe"
	body := "d8:announce9:track-url13:creation datei1700000000e4:info" + infoDict + "5:nodesli100eee"
	got, err := extractInfoHash([]byte(body))
	if err != nil {
		t.Fatalf("extractInfoHash: %v", err)
	}
	if want := hashOf(infoDict); got != want {
		t.Errorf("hash = %q, want %q", got, want)
	}
}

func TestExtractInfoHash_NoInfoDict(t *testing.T) {
	t.Parallel()
	body := "d8:announce7:trackere" // no "info" key
	_, err := extractInfoHash([]byte(body))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "info dict") {
		t.Errorf("err = %v, want 'no info dict' message", err)
	}
}

func TestExtractInfoHash_EmptyBody(t *testing.T) {
	t.Parallel()
	_, err := extractInfoHash(nil)
	if err == nil {
		t.Fatal("expected error for empty body")
	}
}

func TestExtractInfoHash_NotADict(t *testing.T) {
	t.Parallel()
	// Top-level value is a list, not a dict — invalid .torrent.
	_, err := extractInfoHash([]byte("li1ee"))
	if err == nil {
		t.Fatal("expected error for non-dict top level")
	}
}

func TestExtractInfoHash_MalformedStringLength(t *testing.T) {
	t.Parallel()
	// "info" key would have an invalid length prefix on the value.
	body := "d4:infodXX:foo3:bare"
	_, err := extractInfoHash([]byte(body))
	if err == nil {
		t.Fatal("expected error for malformed string length")
	}
}

func TestExtractInfoHash_UnterminatedInteger(t *testing.T) {
	t.Parallel()
	// Integer value runs past end of input.
	body := "d4:infoi123"
	_, err := extractInfoHash([]byte(body))
	if err == nil {
		t.Fatal("expected error for unterminated integer")
	}
}
