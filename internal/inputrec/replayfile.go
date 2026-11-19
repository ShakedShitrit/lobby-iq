package inputrec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"unicode/utf16"
)

// ReplayExt ends the files Rocket League saves replays as.
const ReplayExt = ".replay"

// maxReplayHeader caps how much of a replay is read looking for its header.
// Real headers are a few kilobytes; anything claiming far more isn't one.
const maxReplayHeader = 4 << 20

// ReplayInfo is what a saved replay says about itself that matters here.
type ReplayInfo struct {
	// File is the replay's file name, e.g. "A7C76BF4...9E.replay".
	File string
	// Name is what the player called it when saving; often empty.
	Name      string
	MatchGuid string
}

// ReadReplayInfo reads the header of the replay at path.
//
// A replay's header is a list of Unreal properties. Rather than parse the
// whole list - whose array properties vary between game versions - this
// looks for the two string properties it needs by their exact encoding:
// the property's name, then "StrProperty", then the value. Those byte
// sequences can't occur by chance in the rest of the header.
func ReadReplayInfo(path string) (ReplayInfo, error) {
	info := ReplayInfo{File: filepath.Base(path)}

	f, err := os.Open(path)
	if err != nil {
		return info, err
	}
	defer f.Close()

	var prefix [8]byte // header size, then its CRC
	if _, err := io.ReadFull(f, prefix[:]); err != nil {
		return info, err
	}
	size := binary.LittleEndian.Uint32(prefix[:4])
	if size == 0 || size > maxReplayHeader {
		return info, errors.New("not a replay header")
	}
	header := make([]byte, size)
	if _, err := io.ReadFull(f, header); err != nil {
		return info, err
	}

	guid, ok := strProperty(header, "MatchGUID")
	if !ok || guid == "" {
		return info, errors.New("replay has no MatchGUID")
	}
	info.MatchGuid = guid
	info.Name, _ = strProperty(header, "ReplayName")
	return info, nil
}

// ueString encodes s as Unreal serialises an ASCII string: its length
// including a terminating NUL, then the bytes, then the NUL.
func ueString(s string) []byte {
	b := make([]byte, 4, 4+len(s)+1)
	binary.LittleEndian.PutUint32(b, uint32(len(s)+1))
	b = append(b, s...)
	return append(b, 0)
}

// strProperty finds the StrProperty called name in header and returns its
// value.
func strProperty(header []byte, name string) (string, bool) {
	needle := append(ueString(name), ueString("StrProperty")...)
	i := bytes.Index(header, needle)
	if i < 0 {
		return "", false
	}
	rest := header[i+len(needle):]
	if len(rest) < 12 {
		return "", false
	}
	rest = rest[8:] // the value's size, which the string repeats
	n := int32(binary.LittleEndian.Uint32(rest[:4]))
	rest = rest[4:]
	switch {
	case n == 0:
		return "", true
	case n > 0:
		// ASCII, NUL-terminated.
		if int(n) > len(rest) {
			return "", false
		}
		return string(bytes.TrimRight(rest[:n], "\x00")), true
	default:
		// UTF-16, for names typed with characters outside ASCII.
		count := int(-n)
		if count*2 > len(rest) {
			return "", false
		}
		units := make([]uint16, count)
		for j := range units {
			units[j] = binary.LittleEndian.Uint16(rest[2*j:])
		}
		for len(units) > 0 && units[len(units)-1] == 0 {
			units = units[:len(units)-1]
		}
		return string(utf16.Decode(units)), true
	}
}
