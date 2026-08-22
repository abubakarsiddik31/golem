package bedrock

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"strings"
	"testing"
)

// buildFrame encodes one event-stream frame with string headers, the
// same wire format readStreamMessage decodes.
func buildFrame(headers [][2]string, payload string) []byte {
	var block bytes.Buffer
	for _, header := range headers {
		block.WriteByte(byte(len(header[0])))
		block.WriteString(header[0])
		block.WriteByte(7) // string value
		binary.Write(&block, binary.BigEndian, uint16(len(header[1])))
		block.WriteString(header[1])
	}
	prelude := make([]byte, 8)
	binary.BigEndian.PutUint32(prelude[0:4], uint32(16+block.Len()+len(payload)))
	binary.BigEndian.PutUint32(prelude[4:8], uint32(block.Len()))
	hasher := crc32.NewIEEE()
	hasher.Write(prelude)
	hasher.Write(block.Bytes())
	hasher.Write([]byte(payload))

	var frame bytes.Buffer
	frame.Write(prelude)
	binary.Write(&frame, binary.BigEndian, crc32.ChecksumIEEE(prelude))
	frame.Write(block.Bytes())
	frame.Write([]byte(payload))
	binary.Write(&frame, binary.BigEndian, hasher.Sum32())
	return frame.Bytes()
}

func TestReadStreamMessageDecodesFrames(t *testing.T) {
	first := buildFrame([][2]string{
		{":message-type", "event"},
		{":event-type", "contentBlockDelta"},
	}, `{"delta":{"text":"hi"}}`)
	second := buildFrame([][2]string{
		{":message-type", "exception"},
		{":exception-type", "throttlingException"},
	}, `{"message":"slow down"}`)
	reader := bufio.NewReader(bytes.NewReader(append(append([]byte{}, first...), second...)))

	message, err := readStreamMessage(reader)
	if err != nil {
		t.Fatalf("first frame error = %v", err)
	}
	if message.Headers[":event-type"] != "contentBlockDelta" || string(message.Payload) != `{"delta":{"text":"hi"}}` {
		t.Errorf("first frame = %+v %s", message.Headers, message.Payload)
	}
	message, err = readStreamMessage(reader)
	if err != nil {
		t.Fatalf("second frame error = %v", err)
	}
	if message.Headers[":exception-type"] != "throttlingException" {
		t.Errorf("second frame headers = %v", message.Headers)
	}
	if _, err := readStreamMessage(reader); err == nil || !strings.Contains(err.Error(), "EOF") {
		t.Errorf("end of stream error = %v, want EOF", err)
	}
}

func TestReadStreamMessageSkipsNonStringHeaders(t *testing.T) {
	// Hand-build a headers block mixing value types: bool, int32, and a
	// 2-byte-length-prefixed byte string before the string headers.
	var block bytes.Buffer
	writeHeader := func(name string, raw func(*bytes.Buffer)) {
		block.WriteByte(byte(len(name)))
		block.WriteString(name)
		raw(&block)
	}
	writeHeader("bool-true", func(b *bytes.Buffer) { b.WriteByte(0) })
	writeHeader("int32", func(b *bytes.Buffer) { b.WriteByte(4); b.Write([]byte{0, 0, 0, 7}) })
	writeHeader("bytes", func(b *bytes.Buffer) {
		b.WriteByte(6)
		binary.Write(b, binary.BigEndian, uint16(3))
		b.Write([]byte{1, 2, 3})
	})
	writeHeader(":event-type", func(b *bytes.Buffer) {
		value := "messageStop"
		b.WriteByte(7)
		binary.Write(b, binary.BigEndian, uint16(len(value)))
		b.WriteString(value)
	})

	prelude := make([]byte, 8)
	binary.BigEndian.PutUint32(prelude[0:4], uint32(16+block.Len()))
	binary.BigEndian.PutUint32(prelude[4:8], uint32(block.Len()))
	hasher := crc32.NewIEEE()
	hasher.Write(prelude)
	hasher.Write(block.Bytes())

	var frame bytes.Buffer
	frame.Write(prelude)
	binary.Write(&frame, binary.BigEndian, crc32.ChecksumIEEE(prelude))
	frame.Write(block.Bytes())
	binary.Write(&frame, binary.BigEndian, hasher.Sum32())

	message, err := readStreamMessage(bufio.NewReader(&frame))
	if err != nil {
		t.Fatalf("readStreamMessage error = %v", err)
	}
	if message.Headers[":event-type"] != "messageStop" {
		t.Errorf("headers = %v, want the string header kept", message.Headers)
	}
	if len(message.Payload) != 0 {
		t.Errorf("payload = %q, want empty", message.Payload)
	}
}

func TestReadStreamMessageRejectsCorruption(t *testing.T) {
	frame := buildFrame([][2]string{{":event-type", "messageStop"}}, `{}`)
	corrupt := append([]byte{}, frame...)
	corrupt[len(corrupt)-5] ^= 0xff // flip a payload byte, breaking the message CRC
	if _, err := readStreamMessage(bufio.NewReader(bytes.NewReader(corrupt))); err == nil || !strings.Contains(err.Error(), "CRC") {
		t.Errorf("corrupt frame error = %v, want CRC mismatch", err)
	}

	truncated := frame[:len(frame)-3]
	if _, err := readStreamMessage(bufio.NewReader(bytes.NewReader(truncated))); err == nil {
		t.Error("truncated frame should fail")
	}

	badLength := buildFrame([][2]string{{":event-type", "messageStop"}}, `{}`)
	badLength[0] = 0xff // length out of bounds
	if _, err := readStreamMessage(bufio.NewReader(bytes.NewReader(badLength))); err == nil {
		t.Error("out-of-bounds length should fail")
	}
}
