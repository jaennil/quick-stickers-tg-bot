package fileid

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
)

const (
	fileReferenceFlag = 1 << 25
)

// rleDecode decodes null-byte run-length encoding used by Telegram
// When a zero byte is encountered, the next byte indicates how many zeros to insert
func rleDecode(data []byte) []byte {
	var result []byte
	zero := false
	for _, b := range data {
		if b == 0 {
			zero = true
			continue
		}
		if zero {
			for i := 0; i < int(b); i++ {
				result = append(result, 0)
			}
			zero = false
		} else {
			result = append(result, b)
		}
	}
	return result
}

// DecodeDocumentID extracts the document_id from a Telegram Bot API file_id.
// Structure after RLE decode (for documents/stickers):
//   - Last 1-2 bytes: version info (strip these first)
//   - Bytes 0-3: file_type with flags (little-endian int32)
//   - Bytes 4-7: dc_id (little-endian int32)
//   - If FILE_REFERENCE_FLAG set: variable length file_reference
//   - Then: media_id (8 bytes) + access_hash (8 bytes)
func DecodeDocumentID(fileID string) (int64, error) {
	// Telegram uses URL-safe base64 without padding
	s := strings.ReplaceAll(fileID, "-", "+")
	s = strings.ReplaceAll(s, "_", "/")

	// Add padding if needed
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}

	rawData, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return 0, err
	}

	// RLE decode
	data := rleDecode(rawData)

	if len(data) < 17 {
		return 0, errors.New("file_id too short")
	}

	// Strip version bytes from end (1 byte for version >= 4, 2 bytes otherwise)
	// Last byte is always version
	version := data[len(data)-1]
	if version >= 4 {
		data = data[:len(data)-2] // Strip version + subversion
	} else {
		data = data[:len(data)-1] // Strip just version
	}

	if len(data) < 16 {
		return 0, errors.New("file_id too short after version strip")
	}

	// Read file_type (first 4 bytes)
	fileType := binary.LittleEndian.Uint32(data[0:4])

	// Check for file reference flag
	hasFileRef := (fileType & fileReferenceFlag) != 0

	// Offset starts after file_type (4) + dc_id (4) = 8
	offset := 8

	// If file reference flag is set, skip the file reference
	// TL bytes encoding: length byte (or 254 + 3-byte length), data, then padding to 4-byte boundary
	if hasFileRef {
		if len(data) < offset+1 {
			return 0, errors.New("file_id too short for file reference length")
		}
		// File reference length
		fileRefLen := int(data[offset])
		lengthPrefixSize := 1
		offset += 1

		if fileRefLen == 254 {
			// Long form: 3 bytes for length (little-endian)
			if len(data) < offset+3 {
				return 0, errors.New("file_id too short for long file reference length")
			}
			fileRefLen = int(data[offset]) | int(data[offset+1])<<8 | int(data[offset+2])<<16
			offset += 3
			lengthPrefixSize = 4 // 1 byte (254) + 3 bytes (length)
		}

		offset += fileRefLen

		// Align to 4-byte boundary (padding)
		// The padding aligns the entire structure (length prefix + data), not just data
		padding := (4 - ((lengthPrefixSize + fileRefLen) % 4)) % 4
		offset += padding
	}

	if len(data) < offset+8 {
		return 0, errors.New("file_id too short for document_id")
	}

	// Read document_id (media_id)
	docID := binary.LittleEndian.Uint64(data[offset : offset+8])

	return int64(docID), nil
}
