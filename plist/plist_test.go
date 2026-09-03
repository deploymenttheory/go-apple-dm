package plist_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/plist"
)

type sample struct {
	Name  string `plist:"Name"`
	Count int64  `plist:"Count,omitempty"`
	Flag  bool   `plist:"Flag"`
	Items []string
}

// binaryFixture is "bplist00" encoding of {"A": "b"} produced by macOS plutil.
var binaryFixture = []byte{
	0x62, 0x70, 0x6c, 0x69, 0x73, 0x74, 0x30, 0x30, 0xd1, 0x01, 0x02, 0x51, 0x41, 0x51, 0x62, 0x08,
	0x0b, 0x0d, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x03, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x0f,
}

func TestRoundTripXML(t *testing.T) {
	t.Parallel()
	in := sample{Name: "n", Count: 3, Flag: true, Items: []string{"a", "b"}}
	data, err := plist.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if plist.DetectFormat(data) != plist.FormatXML {
		t.Fatalf("expected XML format, got %v", plist.DetectFormat(data))
	}
	var out sample
	if err := plist.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.Name != "n" || out.Count != 3 || !out.Flag || len(out.Items) != 2 {
		t.Fatalf("round trip mismatch: %+v", out)
	}
	indented, err := plist.MarshalIndent(in, "  ")
	if err != nil || !bytes.Contains(indented, []byte("\n  ")) {
		t.Fatalf("MarshalIndent: %v %q", err, indented)
	}
}

func TestBinaryDecode(t *testing.T) {
	t.Parallel()
	if plist.DetectFormat(binaryFixture) != plist.FormatBinary {
		t.Fatal("binary not detected")
	}
	var m map[string]string
	if err := plist.Unmarshal(binaryFixture, &m); err != nil {
		t.Fatalf("binary unmarshal: %v", err)
	}
	if m["A"] != "b" {
		t.Fatalf("got %v", m)
	}
}

func TestDetectFormat(t *testing.T) {
	t.Parallel()
	cases := map[string]plist.Format{
		"":                            plist.FormatUnknown,
		"hello":                       plist.FormatUnknown,
		"<?xml version=\"1.0\"?>":     plist.FormatXML,
		"  \n<plist version=\"1.0\">": plist.FormatXML,
		"\xef\xbb\xbf<!DOCTYPE plist": plist.FormatXML,
		"bplist00":                    plist.FormatBinary,
	}
	for in, want := range cases {
		if got := plist.DetectFormat([]byte(in)); got != want {
			t.Errorf("DetectFormat(%q) = %v, want %v", in, got, want)
		}
	}
	for _, f := range []plist.Format{plist.FormatXML, plist.FormatBinary, plist.FormatUnknown, plist.Format(99)} {
		if f.String() == "" {
			t.Errorf("empty String for %d", f)
		}
	}
}

func TestLimits(t *testing.T) {
	t.Parallel()
	var v map[string]any
	big := []byte("<plist>" + strings.Repeat(" ", 100) + "</plist>")
	err := plist.Decoder{MaxBytes: 10}.Unmarshal(big, &v)
	if !errors.Is(err, plist.ErrTooLarge) {
		t.Fatalf("expected ErrTooLarge, got %v", err)
	}
	deep := "<plist version=\"1.0\">" + strings.Repeat("<array>", 5) + strings.Repeat("</array>", 5) + "</plist>"
	err = plist.Decoder{MaxDepth: 3}.Unmarshal([]byte(deep), &v)
	if !errors.Is(err, plist.ErrTooDeep) {
		t.Fatalf("expected ErrTooDeep, got %v", err)
	}
	// Disabled limits accept the same input.
	var anyv any
	if err := (plist.Decoder{MaxBytes: -1, MaxDepth: -1}).Unmarshal([]byte(deep), &anyv); err != nil {
		t.Fatalf("disabled limits: %v", err)
	}
	if err := plist.Unmarshal([]byte("not a plist"), &v); !errors.Is(err, plist.ErrUnknownFormat) {
		t.Fatalf("expected ErrUnknownFormat, got %v", err)
	}
	// Malformed XML passes the depth scan and fails in the real decoder.
	if err := plist.Unmarshal([]byte("<plist><dict><key>a</key></plist>"), &v); err == nil {
		t.Fatal("expected decode error for malformed XML")
	}
	if err := plist.Unmarshal(append([]byte("bplist00"), 0xff), &v); err == nil {
		t.Fatal("expected error for truncated binary plist")
	}
}

func TestMarshalErrors(t *testing.T) {
	t.Parallel()
	if _, err := plist.Marshal(make(chan int)); err == nil {
		t.Fatal("expected error marshalling a channel")
	}
	if _, err := plist.MarshalIndent(make(chan int), " "); err == nil {
		t.Fatal("expected error marshalling a channel")
	}
}

func FuzzUnmarshal(f *testing.F) {
	f.Add([]byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>A</key><string>b</string></dict></plist>`))
	f.Add(binaryFixture)
	f.Add([]byte("bplist00"))
	f.Fuzz(func(t *testing.T, data []byte) {
		var v any
		_ = plist.Decoder{MaxBytes: 1 << 16}.Unmarshal(data, &v)
	})
}
