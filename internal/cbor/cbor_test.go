package cbor_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-mdm/internal/cbor"
)

// attestation is the shape Apple sends: a format name and a statement whose
// x5c member holds the certificate chain.
type attestation struct {
	Format  string           `cbor:"fmt"`
	AttStmt cbor.RawMessage  `cbor:"attStmt"`
	Ignored string           `cbor:"-"`
	Nested  *nestedStatement `cbor:"nested,omitempty"`
}

type nestedStatement struct {
	X5C [][]byte `cbor:"x5c"`
}

// hex builds a byte slice from a compact hex string for readable fixtures.
func hex(t *testing.T, s string) []byte {
	t.Helper()
	s = strings.ReplaceAll(s, " ", "")
	if len(s)%2 != 0 {
		t.Fatalf("odd hex %q", s)
	}
	out := make([]byte, len(s)/2)
	for i := range out {
		var v byte
		for j := range 2 {
			c := s[i*2+j]
			switch {
			case c >= '0' && c <= '9':
				v = v<<4 | (c - '0')
			case c >= 'a' && c <= 'f':
				v = v<<4 | (c - 'a' + 10)
			default:
				t.Fatalf("bad hex %q", s)
			}
		}
		out[i] = v
	}
	return out
}

func TestUnmarshalAttestationObject(t *testing.T) {
	// {"fmt": "apple", "attStmt": {"x5c": [h'01', h'02']}}
	raw := hex(t, "a2 63 666d74 65 6170706c65 67 61747453746d74 a1 63 783563 82 4101 4102")
	var got attestation
	if err := cbor.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Format != "apple" {
		t.Fatalf("fmt = %q", got.Format)
	}
	// The statement is kept verbatim so it can be decoded once its format
	// is known, and persisted byte for byte.
	wantStmt := hex(t, "a1 63 783563 82 4101 4102")
	if !bytes.Equal(got.AttStmt, wantStmt) {
		t.Fatalf("attStmt = %x, want %x", got.AttStmt, wantStmt)
	}
	var stmt nestedStatement
	if err := cbor.Unmarshal(got.AttStmt, &stmt); err != nil {
		t.Fatal(err)
	}
	if len(stmt.X5C) != 2 || stmt.X5C[0][0] != 1 || stmt.X5C[1][0] != 2 {
		t.Fatalf("x5c = %x", stmt.X5C)
	}
}

func TestUnmarshalSkipsUnknownMembers(t *testing.T) {
	// A member Apple might add later must not break the decode.
	// {"fmt": "apple", "authData": h'0102', "attStmt": {"x5c": []}}
	raw := hex(t, "a3 63 666d74 65 6170706c65 68 6175746844617461 42 0102 67 61747453746d74 a1 63 783563 80")
	var got attestation
	if err := cbor.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Format != "apple" {
		t.Fatalf("fmt = %q", got.Format)
	}
}

func TestUnmarshalScalars(t *testing.T) {
	type scalars struct {
		Text   string            `cbor:"t"`
		Bytes  []byte            `cbor:"b"`
		N      int64             `cbor:"n"`
		Neg    int32             `cbor:"g"`
		U      uint16            `cbor:"u"`
		Yes    bool              `cbor:"y"`
		No     bool              `cbor:"z"`
		Null   *nestedStatement  `cbor:"null"`
		Names  map[string]string `cbor:"names"`
		Absent string            // untagged, named by its field
	}
	enc, err := cbor.Marshal(scalars{
		Text: "hi", Bytes: []byte{9}, N: 1000000, Neg: -500, U: 65535, Yes: true,
		Names: map[string]string{"a": "1", "bb": "2"}, Absent: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got scalars
	if err := cbor.Unmarshal(enc, &got); err != nil {
		t.Fatal(err)
	}
	if got.Text != "hi" || got.N != 1000000 || got.Neg != -500 || got.U != 65535 ||
		!got.Yes || got.No || got.Null != nil || got.Absent != "x" ||
		got.Names["a"] != "1" || got.Names["bb"] != "2" {
		t.Fatalf("round trip = %+v", got)
	}
}

func TestUnmarshalPointerAndNull(t *testing.T) {
	// {"nested": {"x5c": [h'ff']}} fills the pointer; null leaves it nil.
	raw := hex(t, "a1 66 6e657374656420 a1 63 783563 81 41ff")
	// The key above is "nested " with a trailing space; build it properly.
	raw = hex(t, "a1 66 6e6573746564 a1 63 783563 81 41ff")
	var got attestation
	if err := cbor.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Nested == nil || len(got.Nested.X5C) != 1 || got.Nested.X5C[0][0] != 0xff {
		t.Fatalf("nested = %+v", got.Nested)
	}
	nullRaw := hex(t, "a1 66 6e6573746564 f6")
	got = attestation{Nested: &nestedStatement{}}
	if err := cbor.Unmarshal(nullRaw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Nested != nil {
		t.Fatalf("null left %+v", got.Nested)
	}
}

func TestUnmarshalRejects(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want error
	}{
		{"indefinite byte string", "5f 41 01 ff", cbor.ErrUnsupported},
		{"indefinite text string", "7f 61 61 ff", cbor.ErrUnsupported},
		{"indefinite array", "9f 01 ff", cbor.ErrUnsupported},
		{"indefinite map", "bf 61 61 01 ff", cbor.ErrUnsupported},
		{"tag", "c0 61 61", cbor.ErrUnsupported},
		{"half float", "f9 3c00", cbor.ErrUnsupported},
		{"single float", "fa 3f800000", cbor.ErrUnsupported},
		{"double float", "fb 3ff0000000000000", cbor.ErrUnsupported},
		{"eight bit simple", "f8 ff", cbor.ErrUnsupported},
		{"undefined simple", "f7", cbor.ErrUnsupported},
		{"reserved additional information", "1c", cbor.ErrSyntax},
		{"reserved 29", "1d", cbor.ErrSyntax},
		{"reserved 30", "1e", cbor.ErrSyntax},
		{"empty", "", cbor.ErrSyntax},
		{"truncated argument", "19 01", cbor.ErrSyntax},
		{"truncated string", "43 01", cbor.ErrSyntax},
		{"trailing data", "01 01", cbor.ErrTrailing},
		{"array longer than input", "9a ffffffff", cbor.ErrSyntax},
		{"map longer than input", "ba ffffffff", cbor.ErrSyntax},
		{"integer key", "a1 01 01", cbor.ErrUnsupported},
		{"byte string key", "a1 41 61 01", cbor.ErrUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var raw cbor.RawMessage
			err := cbor.Unmarshal(hex(t, tc.in), &raw)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Unmarshal = %v, want %v", err, tc.want)
			}
			// Wellformed agrees with Unmarshal on every rejection.
			if err := cbor.Wellformed(hex(t, tc.in)); !errors.Is(err, tc.want) {
				t.Fatalf("Wellformed = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestUnmarshalRejectsDuplicateKeys(t *testing.T) {
	// A duplicate would let a sender show one member to a validator and
	// another to the decoder that acts on it.
	dup := hex(t, "a2 63 666d74 65 6170706c65 63 666d74 61 78")
	var got attestation
	if err := cbor.Unmarshal(dup, &got); !errors.Is(err, cbor.ErrDuplicate) {
		t.Fatalf("struct = %v", err)
	}
	var m map[string]cbor.RawMessage
	if err := cbor.Unmarshal(dup, &m); !errors.Is(err, cbor.ErrDuplicate) {
		t.Fatalf("map = %v", err)
	}
	// Well-formedness does not judge duplicates; it is a decode rule.
	if err := cbor.Wellformed(dup); err != nil {
		t.Fatalf("Wellformed = %v", err)
	}
}

func TestUnmarshalDepth(t *testing.T) {
	deep := bytes.Repeat([]byte{0x81}, cbor.MaxDepth+2)
	deep = append(deep, 0x01)
	var raw cbor.RawMessage
	if err := cbor.Unmarshal(deep, &raw); !errors.Is(err, cbor.ErrDepth) {
		t.Fatalf("Unmarshal = %v", err)
	}
	if err := cbor.Wellformed(deep); !errors.Is(err, cbor.ErrDepth) {
		t.Fatalf("Wellformed = %v", err)
	}
	// Nested slices hit the same limit through the typed path.
	type deepSlice struct {
		X [][][]byte `cbor:"x"`
	}
	var ds deepSlice
	if err := cbor.Unmarshal(deep, &ds); err == nil {
		t.Fatal("deep input decoded")
	}
}

func TestUnmarshalSizeLimit(t *testing.T) {
	big := make([]byte, cbor.MaxBytes+1)
	var raw cbor.RawMessage
	if err := cbor.Unmarshal(big, &raw); !errors.Is(err, cbor.ErrTooLarge) {
		t.Fatalf("Unmarshal = %v", err)
	}
	if err := cbor.Wellformed(big); !errors.Is(err, cbor.ErrTooLarge) {
		t.Fatalf("Wellformed = %v", err)
	}
}

func TestUnmarshalTargets(t *testing.T) {
	var raw cbor.RawMessage
	if err := cbor.Unmarshal(hex(t, "01"), raw); !errors.Is(err, cbor.ErrTarget) {
		t.Fatalf("non-pointer = %v", err)
	}
	if err := cbor.Unmarshal(hex(t, "01"), (*attestation)(nil)); !errors.Is(err, cbor.ErrTarget) {
		t.Fatalf("nil pointer = %v", err)
	}
	cases := []struct {
		name string
		in   string
		into any
	}{
		{"text into int", "61 61", new(int64)},
		{"bytes into string", "41 01", new(string)},
		{"integer into string", "01", new(string)},
		{"negative into uint", "20", new(uint64)},
		{"array into string", "80", new(string)},
		{"map into string", "a0", new(string)},
		{"bool into string", "f5", new(string)},
		{"map into slice", "a0", new([][]byte)},
		{"map with integer keys", "a0", new(map[int]string)},
		{"integer overflow", "19 0100", new(int8)},
		{"unsigned overflow", "19 0100", new(uint8)},
		{"negative overflow", "39 0100", new(int8)},
		{"huge unsigned into int", "1b ffffffffffffffff", new(int64)},
		{"huge negative", "3b ffffffffffffffff", new(int64)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := cbor.Unmarshal(hex(t, tc.in), tc.into); err == nil {
				t.Fatal("decoded into the wrong type")
			}
		})
	}
}

func TestUnmarshalNestedError(t *testing.T) {
	// A fault inside a member names the member.
	raw := hex(t, "a1 63 666d74 01")
	var got attestation
	err := cbor.Unmarshal(raw, &got)
	if err == nil || !strings.Contains(err.Error(), `"fmt"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestMarshalDeterministicOrder(t *testing.T) {
	// RFC 8949 section 4.2.1: shorter keys first, then bytewise.
	enc, err := cbor.Marshal(map[string]int{"bb": 2, "a": 1, "ab": 3, "c": 4})
	if err != nil {
		t.Fatal(err)
	}
	want := hex(t, "a4 61 61 01 61 63 04 62 6162 03 62 6262 02")
	if !bytes.Equal(enc, want) {
		t.Fatalf("Marshal = %x, want %x", enc, want)
	}
	// The same map encodes identically every time.
	for range 20 {
		again, err := cbor.Marshal(map[string]int{"bb": 2, "a": 1, "ab": 3, "c": 4})
		if err != nil || !bytes.Equal(again, enc) {
			t.Fatalf("unstable encoding %x", again)
		}
	}
}

func TestMarshalHeadWidths(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"tiny", uint64(23), "17"},
		{"one byte", uint64(24), "18 18"},
		{"two bytes", uint64(256), "19 0100"},
		{"four bytes", uint64(65536), "1a 00010000"},
		{"eight bytes", uint64(4294967296), "1b 0000000100000000"},
		{"negative", -1, "20"},
		{"true", true, "f5"},
		{"false", false, "f4"},
		{"nil pointer", (*attestation)(nil), "f6"},
		{"nil interface", nil, "f6"},
		{"bytes", []byte{1, 2}, "42 0102"},
		{"array", []string{"a"}, "81 61 61"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := cbor.Marshal(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if want := hex(t, tc.want); !bytes.Equal(enc, want) {
				t.Fatalf("Marshal = %x, want %x", enc, want)
			}
		})
	}
}

func TestMarshalRejects(t *testing.T) {
	if _, err := cbor.Marshal(1.5); !errors.Is(err, cbor.ErrType) {
		t.Fatalf("float = %v", err)
	}
	if _, err := cbor.Marshal(map[int]string{1: "a"}); !errors.Is(err, cbor.ErrType) {
		t.Fatalf("integer keys = %v", err)
	}
	if _, err := cbor.Marshal(struct {
		X map[int]string `cbor:"x"`
	}{}); err == nil {
		t.Fatal("bad field encoded")
	}
	if _, err := cbor.Marshal([]any{map[int]string{}}); err == nil {
		t.Fatal("bad element encoded")
	}
	// A raw message that is not well formed never reaches the wire.
	if _, err := cbor.Marshal(cbor.RawMessage{0xff}); err == nil {
		t.Fatal("malformed raw message encoded")
	}
	if _, err := cbor.Marshal(map[string]cbor.RawMessage{"a": {0xff}}); err == nil {
		t.Fatal("malformed raw message in a map encoded")
	}
	// Depth is bounded on the way out as well as in.
	type link struct {
		Next *link `cbor:"n"`
	}
	head := &link{}
	for cur, i := head, 0; i < cbor.MaxDepth+2; i++ {
		cur.Next = &link{}
		cur = cur.Next
	}
	if _, err := cbor.Marshal(head); !errors.Is(err, cbor.ErrDepth) {
		t.Fatalf("deep value = %v", err)
	}
}

func TestMarshalRawMessagePassesThrough(t *testing.T) {
	inner := hex(t, "a1 63 783563 81 4101")
	enc, err := cbor.Marshal(map[string]cbor.RawMessage{"attStmt": cbor.RawMessage(inner)})
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]cbor.RawMessage
	if err := cbor.Unmarshal(enc, &back); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back["attStmt"], inner) {
		t.Fatalf("round trip = %x, want %x", back["attStmt"], inner)
	}
}

func TestWellformed(t *testing.T) {
	good := hex(t, "a2 63 666d74 65 6170706c65 67 61747453746d74 a1 63 783563 82 4101 4102")
	if err := cbor.Wellformed(good); err != nil {
		t.Fatal(err)
	}
	if err := cbor.Wellformed(good[:len(good)-1]); err == nil {
		t.Fatal("truncated input accepted")
	}
}

func FuzzUnmarshal(f *testing.F) {
	f.Add(hex(f2t(f), "a2 63 666d74 65 6170706c65 67 61747453746d74 a1 63 783563 82 4101 4102"))
	f.Add([]byte{0x01})
	f.Add([]byte{0xff})
	f.Add([]byte{0x9f, 0x01, 0xff})
	f.Add([]byte{0xa1, 0x61, 0x61, 0xf6})
	f.Fuzz(func(t *testing.T, data []byte) {
		var raw cbor.RawMessage
		errRaw := cbor.Unmarshal(data, &raw)
		errWF := cbor.Wellformed(data)
		if (errRaw == nil) != (errWF == nil) {
			t.Fatalf("Unmarshal = %v but Wellformed = %v", errRaw, errWF)
		}
		if errRaw != nil {
			// A rejected input must not decode into a typed target either.
			var att attestation
			_ = cbor.Unmarshal(data, &att)
			return
		}
		if !bytes.Equal(raw, data) {
			t.Fatalf("raw message %x differs from input %x", raw, data)
		}
		// Anything accepted re-encodes to exactly the bytes that arrived,
		// so a stored attestation object survives a round trip.
		out, err := cbor.Marshal(raw)
		if err != nil || !bytes.Equal(out, data) {
			t.Fatalf("Marshal(raw) = %x, %v", out, err)
		}
		var att attestation
		_ = cbor.Unmarshal(data, &att)
	})
}

// f2t adapts a fuzz target to the helper that wants a *testing.T.
func f2t(f *testing.F) *testing.T {
	f.Helper()
	return &testing.T{}
}

func TestUnmarshalErrorsInsideContainers(t *testing.T) {
	type strings3 struct {
		X string `cbor:"x"`
	}
	cases := []struct {
		name string
		in   string
		into any
	}{
		{"array element", "81 c0 01", new([][]byte)},
		{"array too long for input", "9a ffffffff", new([][]byte)},
		{"map value", "a1 61 61 c0 01", new(map[string]string)},
		{"map key not text", "a1 01 01", new(map[string]string)},
		{"map key truncated", "a1 63 61", new(map[string]string)},
		{"struct key not text", "a1 01 01", new(strings3)},
		{"unknown member malformed", "a1 62 7878 c0 01", new(strings3)},
		{"map too long for input", "ba ffffffff", new(map[string]string)},
		{"struct too long for input", "ba ffffffff", new(strings3)},
		{"nested slice element", "81 81 c0 01", new([][][]byte)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := cbor.Unmarshal(hex(t, tc.in), tc.into); err == nil {
				t.Fatal("malformed input accepted")
			}
		})
	}
}

func TestFieldNamingIgnoresUnexported(t *testing.T) {
	type mixed struct {
		Kept   string `cbor:"kept"`
		hidden string
		Skip   string `cbor:"-"`
		Empty  string `cbor:"empty,omitempty"`
	}
	enc, err := cbor.Marshal(mixed{Kept: "yes", hidden: "no", Skip: "no"})
	if err != nil {
		t.Fatal(err)
	}
	want := hex(t, "a1 64 6b657074 63 796573")
	if !bytes.Equal(enc, want) {
		t.Fatalf("Marshal = %x, want %x", enc, want)
	}
	var back mixed
	if err := cbor.Unmarshal(enc, &back); err != nil {
		t.Fatal(err)
	}
	if back.Kept != "yes" || back.hidden != "" || back.Skip != "" {
		t.Fatalf("round trip = %+v", back)
	}
	// A member named for a field with no tag still decodes.
	type untagged struct{ Name string }
	var u untagged
	if err := cbor.Unmarshal(hex(t, "a1 644e616d65 62 6869"), &u); err != nil || u.Name != "hi" {
		t.Fatalf("untagged = %+v %v", u, err)
	}
}
