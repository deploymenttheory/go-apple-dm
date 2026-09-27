package applications

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
)

// Resource is the package-oriented export view. Editable metadata is flattened,
// while hashes, format and size come only from verified content. ExpectedDigests
// preserves upload constraints separately. Size is an exact byte count, not a
// rounded display string. Indexed is null until a payload index is available.
type Resource struct {
	Metadata
	ID                  string  `json:"id"`
	Indexed             *bool   `json:"indexed"`
	CloudTransferStatus string  `json:"cloudTransferStatus"`
	Size                *int64  `json:"size"`
	MD5                 string  `json:"md5,omitempty"`
	SHA256              string  `json:"sha256,omitempty"`
	SHA3512             string  `json:"sha3512,omitempty"`
	SHA512              string  `json:"sha512,omitempty"`
	HashType            string  `json:"hashType,omitempty"`
	HashValue           string  `json:"hashValue,omitempty"`
	Format              string  `json:"format,omitempty"`
	Manifest            []byte  `json:"manifest,omitempty"`
	ManifestFileName    string  `json:"manifestFileName,omitempty"`
	ExpectedDigests     Digests `json:"expectedDigests"`
}

// Resource returns the export projection without exposing backend object keys.
func (r Record) Resource() Resource {
	out := Resource{Metadata: r.Metadata, ID: r.ID, Indexed: r.Indexed, CloudTransferStatus: r.CloudTransferStatus, ExpectedDigests: r.Metadata.Digests}
	if c := r.Content; c != nil {
		out.Size = &c.Size
		out.MD5 = c.MD5
		out.SHA256 = c.SHA256
		out.SHA3512 = c.SHA3512
		out.SHA512 = c.SHA512
		out.HashType = c.HashType
		out.HashValue = c.HashValue
		out.Format = c.Format
	}
	if r.Manifest != nil {
		out.Manifest = slices.Clone(r.Manifest.Data)
		out.ManifestFileName = r.Manifest.FileName
	}
	return out
}

// OrderForDelivery sorts a copy by priority (default 10), then package ID. This
// orders server-side submission; it cannot impose ordering on asynchronous device
// installs. ParentPackageID describes a relationship, not an implicit dependency.
func OrderForDelivery(records []Record) []Record {
	out := slices.Clone(records)
	priority := func(r Record) int {
		if r.Metadata.Priority == nil {
			return 10
		}
		return *r.Metadata.Priority
	}
	slices.SortStableFunc(out, func(a, b Record) int {
		if priority(a) < priority(b) {
			return -1
		}
		if priority(a) > priority(b) {
			return 1
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return out
}

// ExportJSON writes a caller-selected page of resources, including every field.
// Pagination remains explicit so exports need not load the entire catalogue.
func ExportJSON(w io.Writer, records []Record) error {
	out := make([]Resource, 0, len(records))
	for _, r := range records {
		out = append(out, r.Resource())
	}
	return json.NewEncoder(w).Encode(out)
}

// ExportCSV writes the selected fields in caller order. An empty selection uses
// the complete package schema. Unknown or duplicate fields fail before any output.
// Cells beginning with spreadsheet formula characters are prefixed with an
// apostrophe; use JSON for a lossless machine-readable export.
func ExportCSV(w io.Writer, records []Record, fields []string) error {
	if len(fields) == 0 {
		fields = PackageFields()
	}
	allowed := map[string]bool{}
	for _, f := range PackageFields() {
		allowed[f] = true
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if !allowed[f] || seen[f] {
			return fmt.Errorf("%w: export field %q", ErrInvalid, f)
		}
		seen[f] = true
	}
	writer := csv.NewWriter(w)
	if err := writer.Write(fields); err != nil {
		return err
	}
	for _, r := range records {
		b, err := json.Marshal(r.Resource())
		if err != nil {
			return err
		}
		var object map[string]json.RawMessage
		if err = json.Unmarshal(b, &object); err != nil {
			return err
		}
		row := make([]string, len(fields))
		for i, f := range fields {
			v := object[f]
			if len(v) == 0 || string(v) == "null" {
				continue
			}
			if v[0] == '"' {
				if err = json.Unmarshal(v, &row[i]); err != nil {
					return err
				}
			} else {
				row[i] = string(v)
			}
			if len(row[i]) > 0 {
				switch row[i][0] {
				case '=', '+', '-', '@', '\t', '\r', '\n':
					row[i] = "'" + row[i]
				}
			}
		}
		if err = writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

// PackageFields returns the package export schema, including Apple identity and
// explicit SHA-512 and expected-digest extensions. The returned slice is independent.
func PackageFields() []string {
	return []string{
		"id", "packageName", "fileName", "categoryId", "info", "notes", "priority", "osRequirements",
		"fillUserTemplate", "indexed", "fillExistingUsers", "swu", "rebootRequired", "selfHealNotify", "selfHealingAction",
		"osInstall", "serialNumber", "parentPackageId", "basePath", "suppressUpdates", "cloudTransferStatus", "ignoreConflicts",
		"suppressFromDock", "suppressEula", "suppressRegistration", "installLanguage", "md5", "sha256", "sha3512",
		"hashType", "hashValue", "size", "osInstallerVersion", "manifest", "manifestFileName", "format",
		"bundleID", "version", "sha512", "expectedDigests",
	}
}

// ExportHistoryJSON writes an already-paged history response without dropping metadata.
func ExportHistoryJSON(w io.Writer, entries []HistoryEntry) error {
	return json.NewEncoder(w).Encode(entries)
}

// ExportHistoryCSV writes chronological change metadata in a JSON-valued cell.
func ExportHistoryCSV(w io.Writer, entries []HistoryEntry) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"sequence", "packageId", "revision", "action", "time", "note", "contentRevision", "metadata"}); err != nil {
		return err
	}
	for _, e := range entries {
		b, err := json.Marshal(e.Metadata)
		if err != nil {
			return err
		}
		note := e.Note
		if note != "" {
			switch note[0] {
			case '=', '+', '-', '@', '\t', '\r', '\n':
				note = "'" + note
			}
		}
		if err = cw.Write([]string{strconv.FormatUint(e.Sequence, 10), e.PackageID, e.Revision, e.Action, e.Time.Format("2006-01-02T15:04:05.999999999Z07:00"), note, e.ContentRevision, string(b)}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
