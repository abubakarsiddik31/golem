package bedrock

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

// ConverseStream responses are AWS event-stream encoded: a sequence of
// binary frames, each carrying typed headers and one JSON payload. The
// format is documented across AWS services and implemented by the AWS
// SDKs; this reader is the read-only slice of it the adapter needs.
//
// A frame is a prelude — total length, headers length (both uint32
// big-endian), and a CRC32 of those eight bytes — followed by the
// headers block, the payload, and a trailing CRC32 of everything
// before it. Lengths carry 16 bytes of framing overhead. CRCs are
// CRC32-IEEE, Go's hash/crc32 default. This package never encodes
// frames: the request side of ConverseStream is plain JSON.
const (
	streamMinFrameLength = 16
	streamMaxFrameLength = 16 * 1024 * 1024
)

// streamMessage is one decoded event-stream frame.
type streamMessage struct {
	// Headers maps the frame's string-valued headers by name. The
	// adapter reads :message-type, :event-type, and :exception-type;
	// headers of other value types are skipped, not kept.
	Headers map[string]string
	// Payload is the frame's JSON body.
	Payload []byte
}

// readStreamMessage decodes the next frame from reader, io.EOF at a
// frame boundary marking the stream's end. It mirrors the AWS SDK
// decoder's validation order — prelude bounds, prelude CRC, headers,
// payload, trailing message CRC — so a corrupt or truncated stream
// fails at the first bad frame, never reading past it.
func readStreamMessage(reader *bufio.Reader) (streamMessage, error) {
	var prelude [8]byte
	if _, err := io.ReadFull(reader, prelude[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return streamMessage{}, io.EOF
		}
		return streamMessage{}, fmt.Errorf("read prelude: %w", err)
	}
	totalLength := binary.BigEndian.Uint32(prelude[0:4])
	headersLength := binary.BigEndian.Uint32(prelude[4:8])
	if totalLength < streamMinFrameLength || totalLength > streamMaxFrameLength {
		return streamMessage{}, fmt.Errorf("frame length %d out of bounds", totalLength)
	}
	payloadLength := int64(totalLength - headersLength - streamMinFrameLength)
	if payloadLength < 0 {
		return streamMessage{}, fmt.Errorf("frame headers length %d exceeds frame length %d", headersLength, totalLength)
	}

	var preludeCRC [4]byte
	if _, err := io.ReadFull(reader, preludeCRC[:]); err != nil {
		return streamMessage{}, fmt.Errorf("read prelude CRC: %w", err)
	}
	if binary.BigEndian.Uint32(preludeCRC[:]) != crc32.ChecksumIEEE(prelude[:]) {
		return streamMessage{}, fmt.Errorf("prelude CRC mismatch")
	}

	// The message CRC covers the eight prelude bytes, the headers, and
	// the payload — not the CRC fields themselves.
	hasher := crc32.NewIEEE()
	hasher.Write(prelude[:])
	headers, err := readStreamHeaders(io.TeeReader(io.LimitReader(reader, int64(headersLength)), hasher))
	if err != nil {
		return streamMessage{}, err
	}
	payload := make([]byte, payloadLength)
	if payloadLength > 0 {
		if _, err := io.ReadFull(io.TeeReader(reader, hasher), payload); err != nil {
			return streamMessage{}, fmt.Errorf("read payload: %w", err)
		}
	}
	var messageCRC [4]byte
	if _, err := io.ReadFull(reader, messageCRC[:]); err != nil {
		return streamMessage{}, fmt.Errorf("read message CRC: %w", err)
	}
	if binary.BigEndian.Uint32(messageCRC[:]) != hasher.Sum32() {
		return streamMessage{}, fmt.Errorf("message CRC mismatch")
	}
	return streamMessage{Headers: headers, Payload: payload}, nil
}

// readStreamHeaders parses a headers block: length-prefixed names with
// typed values, spanning the whole block. The block's end is its byte
// boundary — end of input while reading the next name, the same signal
// the AWS SDK treats as completion. Only string values are kept; the
// other documented types are skipped at their exact widths.
func readStreamHeaders(block io.Reader) (map[string]string, error) {
	headers := map[string]string{}
	for {
		nameLength, err := readStreamByte(block)
		if errors.Is(err, io.EOF) {
			return headers, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read header name: %w", err)
		}
		name := make([]byte, nameLength)
		if _, err := io.ReadFull(block, name); err != nil {
			return nil, fmt.Errorf("read header name: %w", err)
		}
		valueType, err := readStreamByte(block)
		if err != nil {
			return nil, fmt.Errorf("read header value type: %w", err)
		}
		value, err := readStreamHeaderValue(block, valueType)
		if err != nil {
			return nil, fmt.Errorf("header %s: %w", name, err)
		}
		if value != "" {
			headers[string(name)] = value
		}
	}
}

// readStreamHeaderValue reads one typed header value, returning it as
// a string; non-string types return "" after skipping their bytes.
func readStreamHeaderValue(block io.Reader, valueType byte) (string, error) {
	switch valueType {
	case 0, 1: // bool true, bool false
		return "", nil
	case 2: // int8
		return "", skipStreamBytes(block, 1)
	case 3: // int16
		return "", skipStreamBytes(block, 2)
	case 4: // int32
		return "", skipStreamBytes(block, 4)
	case 5, 8: // int64, timestamp (epoch millis)
		return "", skipStreamBytes(block, 8)
	case 9: // uuid
		return "", skipStreamBytes(block, 16)
	case 6: // bytes: 2-byte length prefix
		length, err := readStreamUint16(block)
		if err != nil {
			return "", err
		}
		return "", skipStreamBytes(block, int64(length))
	case 7: // string: 2-byte length prefix
		length, err := readStreamUint16(block)
		if err != nil {
			return "", err
		}
		value := make([]byte, length)
		if _, err := io.ReadFull(block, value); err != nil {
			return "", err
		}
		return string(value), nil
	default:
		return "", fmt.Errorf("unknown header value type %d", valueType)
	}
}

// skipStreamBytes discards exactly n bytes; end of input mid-skip is a
// malformed block, not its end.
func skipStreamBytes(block io.Reader, n int64) error {
	_, err := io.CopyN(io.Discard, block, n)
	return err
}

func readStreamByte(block io.Reader) (byte, error) {
	var b [1]byte
	if _, err := io.ReadFull(block, b[:]); err != nil {
		return 0, err
	}
	return b[0], nil
}

func readStreamUint16(block io.Reader) (uint16, error) {
	var b [2]byte
	if _, err := io.ReadFull(block, b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(b[:]), nil
}
