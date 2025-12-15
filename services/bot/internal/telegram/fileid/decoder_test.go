package fileid

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func TestDecodeDocumentID(t *testing.T) {
	// Test with a real sticker file_id (you can replace with your actual file_id)
	testCases := []struct {
		name   string
		fileID string
	}{
		{
			name:   "sticker_1",
			fileID: "CAACAgIAAxkBAAIBOGdP8y9UW_Rch8X1UB4Kj3dMkrNOAAJSUAACwfNxSZ3sQ1Mnu4LHNQQ",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Decode and print all bytes for debugging
			s := strings.ReplaceAll(tc.fileID, "-", "+")
			s = strings.ReplaceAll(s, "_", "/")
			switch len(s) % 4 {
			case 2:
				s += "=="
			case 3:
				s += "="
			}
			rawData, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				t.Fatalf("base64 decode error: %v", err)
			}

			fmt.Printf("Raw data (len=%d): %v\n", len(rawData), rawData)

			data := rleDecode(rawData)
			fmt.Printf("RLE decoded (len=%d): %v\n", len(data), data)

			// Extract document_id
			docID, err := DecodeDocumentID(tc.fileID)
			if err != nil {
				t.Fatalf("DecodeDocumentID error: %v", err)
			}

			fmt.Printf("Document ID: %d\n", docID)
			fmt.Printf("Document ID (unsigned): %d\n", uint64(docID))
		})
	}
}
